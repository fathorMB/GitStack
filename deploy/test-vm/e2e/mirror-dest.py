#!/usr/bin/env python3
"""Destinazione di prova per il passo e8 dell'e2e (GIT-181): un server git
HTTPS minimo che serve i repo bare di una cartella con `git http-backend`,
con autenticazione Basic. Sulla VM non c'e' un runtime di container fuori da
k3s e un pod avrebbe un indirizzo del cluster (bloccato dalla policy di uscita
di core): il server gira quindi come processo dell'utente di test, sull'IP
della VM.

Uso: mirror-dest.py <cartella-repo> <porta> <cert.pem> <chiave.pem> <utente> <token>

Il token e' un valore di prova, usa-e-getta, e compare nella riga di comando:
non riusare questo script fuori dalla VM di test.
"""
import base64
import http.server
import os
import ssl
import subprocess
import sys


def read_chunked(rfile):
    body = b""
    while True:
        size = int(rfile.readline().split(b";")[0].strip() or b"0", 16)
        if size == 0:
            while rfile.readline().strip():
                pass
            return body
        body += rfile.read(size)
        rfile.readline()


def main():
    root, port, cert, key, user, token = sys.argv[1:7]
    want = "Basic " + base64.b64encode((user + ":" + token).encode()).decode()
    backend = subprocess.check_output(["git", "--exec-path"]).decode().strip() + "/git-http-backend"

    ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    ctx.load_cert_chain(cert, key)

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = "HTTP/1.0"

        def setup(self):
            # L'handshake TLS nel thread della richiesta: un client lento non
            # blocca l'accept degli altri.
            self.request.do_handshake()
            super().setup()

        def serve(self):
            if self.headers.get("Authorization") != want:
                self.send_response(401)
                self.send_header("WWW-Authenticate", 'Basic realm="mirror-dest"')
                self.send_header("Content-Length", "0")
                self.end_headers()
                return
            if self.headers.get("Transfer-Encoding", "").lower() == "chunked":
                body = read_chunked(self.rfile)
            else:
                body = self.rfile.read(int(self.headers.get("Content-Length") or 0))
            path, _, query = self.path.partition("?")
            env = {
                "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
                "HOME": os.environ.get("HOME", "/tmp"),
                "GIT_CONFIG_NOSYSTEM": "1",
                "GIT_PROJECT_ROOT": root,
                "GIT_HTTP_EXPORT_ALL": "1",
                "REQUEST_METHOD": self.command,
                "PATH_INFO": path,
                "QUERY_STRING": query,
                "CONTENT_TYPE": self.headers.get("Content-Type", ""),
                "CONTENT_LENGTH": str(len(body)),
                "REMOTE_USER": user,
                "REMOTE_ADDR": self.client_address[0],
            }
            if self.headers.get("Content-Encoding"):
                env["HTTP_CONTENT_ENCODING"] = self.headers["Content-Encoding"]
            if self.headers.get("Git-Protocol"):
                env["GIT_PROTOCOL"] = self.headers["Git-Protocol"]
            res = subprocess.run([backend], input=body, env=env, capture_output=True)
            out = res.stdout
            sep = out.find(b"\r\n\r\n")
            sep_len = 4
            if sep < 0:
                sep = out.find(b"\n\n")
                sep_len = 2
            if sep < 0:
                self.send_response(500)
                self.send_header("Content-Length", "0")
                self.end_headers()
                sys.stderr.write("git-http-backend: risposta senza intestazioni: %r\n" % res.stderr[-500:])
                return
            head, payload = out[:sep], out[sep + sep_len:]
            status = 200
            headers = []
            for line in head.decode("latin-1").splitlines():
                name, _, value = line.partition(":")
                if name.lower() == "status":
                    status = int(value.strip().split()[0])
                else:
                    headers.append((name, value.strip()))
            self.send_response(status)
            for name, value in headers:
                self.send_header(name, value)
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)

        do_GET = serve
        do_POST = serve

    class Server(http.server.ThreadingHTTPServer):
        daemon_threads = True

        def get_request(self):
            sock, addr = self.socket.accept()
            return ctx.wrap_socket(sock, server_side=True, do_handshake_on_connect=False), addr

    Server(("0.0.0.0", int(port)), Handler).serve_forever()


if __name__ == "__main__":
    main()
