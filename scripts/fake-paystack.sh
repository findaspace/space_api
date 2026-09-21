#!/usr/bin/env bash
# Stands in for Paystack's initialize endpoint so the webhook path can be
# exercised without an account. Signature verification is unaffected: that uses
# the local secret, not the network.
python3 -c '
import json, http.server, uuid
class H(http.server.BaseHTTPRequestHandler):
    def do_POST(self):
        n = int(self.headers["Content-Length"])
        req = json.loads(self.rfile.read(n))
        if self.path.startswith("/transferrecipient"):
            data = {"id": 1, "recipient_code": "RCP_" + uuid.uuid4().hex[:12], "active": True}
        elif self.path.startswith("/transfer"):
            data = {"id": 9100, "reference": req.get("reference"),
                    "transfer_code": "TRF_" + uuid.uuid4().hex[:12],
                    "status": "pending", "amount": req.get("amount"), "currency": "GHS"}
        else:
            data = {"authorization_url": "https://checkout.paystack.test/" + req["reference"],
                    "access_code": uuid.uuid4().hex, "reference": req["reference"]}
        body = json.dumps({"status": True, "message": "ok", "data": data}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", 9099), H).serve_forever()
'



