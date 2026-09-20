#!/usr/bin/env bash
# Stands in for Paystack's initialize endpoint so the webhook path can be
# exercised without an account. Signature verification is unaffected: that uses
# the local secret, not the network.
python3 -c '
import json, http.server, uuid
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        ref = json.loads(self.rfile.read(int(self.headers["Content-Length"])))["reference"]
        body = json.dumps({"status": True, "message": "ok", "data": {
            "authorization_url": "https://checkout.paystack.test/" + ref,
            "access_code": uuid.uuid4().hex, "reference": ref}}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", 9099), H).serve_forever()
'
