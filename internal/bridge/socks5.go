package bridge

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

type Socks5Server struct {
	ListenAddr  string
	DialTimeout time.Duration
	KeepAlive   time.Duration
	Logger      *log.Logger
	listener    net.Listener
}

func NewSocks5Server(listenAddr string, dialTimeout, keepAlive time.Duration, logger *log.Logger) *Socks5Server {
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	if keepAlive <= 0 {
		keepAlive = 30 * time.Second
	}
	if logger == nil {
		logger = log.New(os.Stdout, "", log.LstdFlags)
	}
	return &Socks5Server{
		ListenAddr:  listenAddr,
		DialTimeout: dialTimeout,
		KeepAlive:   keepAlive,
		Logger:      logger,
	}
}

func (s *Socks5Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.ListenAddr)
	if err != nil {
		return fmt.Errorf("socks5 listen on %s failed: %w", s.ListenAddr, err)
	}
	s.listener = ln
	s.Logger.Printf("SOCKS5 server listening on %s", ln.Addr().String())

	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				break
			}
			s.Logger.Printf("[WARN] accept connection failed: %v", err)
			continue
		}
		go s.handleConn(conn)
	}
	return nil
}

func (s *Socks5Server) Close() error {
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *Socks5Server) handleConn(c net.Conn) {
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))

	buf := make([]byte, 256)
	// 1. Version & Auth negotiation
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	if buf[0] != 0x05 {
		return
	}
	nmethods := int(buf[1])
	if nmethods <= 0 || nmethods > 255 {
		return
	}
	if _, err := io.ReadFull(c, buf[:nmethods]); err != nil {
		return
	}
	// Reply: version 5, no authentication (0x00)
	if _, err := c.Write([]byte{0x05, 0x00}); err != nil {
		return
	}

	// 2. Request details
	if _, err := io.ReadFull(c, buf[:4]); err != nil {
		return
	}
	if buf[0] != 0x05 || buf[1] != 0x01 { // only CONNECT (0x01) supported
		_, _ = c.Write([]byte{0x05, 0x07, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	var host string
	switch buf[3] {
	case 0x01: // IPv4
		if _, err := io.ReadFull(c, buf[:4]); err != nil {
			return
		}
		host = net.IP(buf[:4]).String()
	case 0x03: // Domain name
		if _, err := io.ReadFull(c, buf[:1]); err != nil {
			return
		}
		dLen := int(buf[0])
		if _, err := io.ReadFull(c, buf[:dLen]); err != nil {
			return
		}
		host = string(buf[:dLen])
	case 0x04: // IPv6
		if _, err := io.ReadFull(c, buf[:16]); err != nil {
			return
		}
		host = net.IP(buf[:16]).String()
	default:
		_, _ = c.Write([]byte{0x05, 0x08, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}

	// Port
	if _, err := io.ReadFull(c, buf[:2]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(buf[:2])
	targetAddr := net.JoinHostPort(host, fmt.Sprint(port))

	dialer := net.Dialer{
		Timeout:   s.DialTimeout,
		KeepAlive: s.KeepAlive,
	}
	target, err := dialer.Dial("tcp", targetAddr)
	if err != nil {
		s.Logger.Printf("[WARN] dial %s failed: %v", targetAddr, err)
		_, _ = c.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer target.Close()

	s.Logger.Printf("[INFO] forward %s -> %s", c.RemoteAddr(), targetAddr)

	// Success response
	if _, err := c.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
		return
	}

	// Clear deadlines for data streaming
	_ = c.SetDeadline(time.Time{})

	errCh := make(chan error, 2)
	go func() {
		_, err := io.Copy(target, c)
		if tcpTarget, ok := target.(*net.TCPConn); ok {
			_ = tcpTarget.CloseWrite()
		}
		errCh <- err
	}()
	go func() {
		_, err := io.Copy(c, target)
		if tcpClient, ok := c.(*net.TCPConn); ok {
			_ = tcpClient.CloseWrite()
		}
		errCh <- err
	}()
	<-errCh
}

func RunSocks5Server(listenAddr string, dialTimeout, keepAlive time.Duration) error {
	srv := NewSocks5Server(listenAddr, dialTimeout, keepAlive, log.New(os.Stdout, "", log.LstdFlags))

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		srv.Logger.Printf("SOCKS5 server shutting down...")
		_ = srv.Close()
		os.Exit(0)
	}()

	return srv.ListenAndServe()
}
