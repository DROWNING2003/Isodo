import socket
import json
import urllib.request
from pathlib import Path
base = Path('/root/zkp/isodo-validation')
for attempt in range(2):
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
        s.settimeout(10)
        s.connect(str(base/'vsock.sock'))
        s.sendall(b'CONNECT 1024\n')
        handshake = b''
        while not handshake.endswith(b'\n'):
            chunk=s.recv(1)
            if not chunk: raise RuntimeError('handshake EOF')
            handshake += chunk
        assert handshake.startswith(b'OK '), handshake
        s.sendall(b'probe\n')
        output=b''
        while True:
            chunk=s.recv(4096)
            if not chunk: break
            output+=chunk
        assert b'ISODO_GUEST_STDOUT\n' in output, output
        assert b'ISODO_GUEST_STDERR\n' in output, output
        assert output.endswith(b'EXIT 7\n'), output
        print(json.dumps({'attempt':attempt+1,'handshake':handshake.decode().strip(),'output':output.decode(),'passed':True}))
