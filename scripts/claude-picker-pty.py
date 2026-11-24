#!/usr/bin/env python3
"""Capture an isolated Claude Code /model picker through a real pseudo-terminal."""

import fcntl
import os
import pty
import select
import signal
import struct
import sys
import termios
import time


if len(sys.argv) < 2:
    raise SystemExit("usage: claude-picker-pty.py CLAUDE_BINARY [ARGS...]")

pid, terminal = pty.fork()
if pid == 0:
    os.execvp(sys.argv[1], sys.argv[1:])

fcntl.ioctl(terminal, termios.TIOCSWINSZ, struct.pack("HHHH", 40, 140, 0, 0))
started = time.monotonic()
sent_model = False
sent_trust = False
sent_stop = False
captured = bytearray()
debug = os.getenv("BRIDGE_CLAUDE_TEST_PICKER_DEBUG") == "1"

try:
    while time.monotonic() - started < 13:
        elapsed = time.monotonic() - started
        if elapsed >= 2 and not sent_trust:
            if debug:
                print("picker-pty: trusting isolated temporary folder", file=sys.stderr, flush=True)
            os.write(terminal, b"\x1b[B\r")
            sent_trust = True
        if elapsed >= 4.5 and not sent_model:
            if debug:
                print("picker-pty: sending /model", file=sys.stderr, flush=True)
            os.write(terminal, b"/model\r")
            sent_model = True
        if elapsed >= 10 and not sent_stop:
            if debug:
                print("picker-pty: sending stop", file=sys.stderr, flush=True)
            os.write(terminal, b"\x03\x04")
            sent_stop = True
        readable, _, _ = select.select([terminal], [], [], 0.1)
        if readable:
            try:
                chunk = os.read(terminal, 65536)
            except OSError:
                break
            if not chunk:
                break
            captured.extend(chunk)
            if len(captured) > 2 * 1024 * 1024:
                del captured[: len(captured) - 2 * 1024 * 1024]
        ended, _ = os.waitpid(pid, os.WNOHANG)
        if ended:
            pid = 0
            break
finally:
    if debug:
        print(f"picker-pty: cleaning up {len(captured)} bytes", file=sys.stderr, flush=True)
    if pid:
        try:
            os.kill(pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        os.waitpid(pid, os.WNOHANG)
    os.close(terminal)

sys.stdout.buffer.write(captured)
