"""Restart only the verified Guest T3 launcher; never signal a reused PID."""
import os
import select
import signal
import sys


def restart(pid_file, executable, grace_seconds=5):
    with open(pid_file) as source:
        pid = int(source.read())
    if pid <= 1:
        raise RuntimeError("invalid T3 launcher PID")
    try:
        handle = os.pidfd_open(pid)
    except ProcessLookupError:
        return
    try:
        try:
            with open("/proc/%d/cmdline" % pid, "rb") as source:
                argv = source.read().split(b"\0")
        except FileNotFoundError:
            return
        if not (argv[0] == os.fsencode(executable) and b"bin.mjs" in argv and b"serve" in argv):
            raise RuntimeError("refusing to signal an unrelated process")
        poller = select.poll()
        poller.register(handle, select.POLLIN)
        try:
            signal.pidfd_send_signal(handle, signal.SIGTERM)
        except ProcessLookupError:
            return
        if poller.poll(int(grace_seconds * 1000)):
            return
        # The pidfd still refers to the verified launcher even if the numeric
        # PID has been reused. A hung shutdown must not block all future links.
        print("T3 graceful shutdown timed out; escalating verified launcher", flush=True)
        try:
            signal.pidfd_send_signal(handle, signal.SIGKILL)
        except ProcessLookupError:
            return
        if not poller.poll(5000):
            raise RuntimeError("T3 launcher did not exit after SIGKILL")
    finally:
        os.close(handle)


if __name__ == "__main__":
    restart(sys.argv[1], sys.argv[2])
