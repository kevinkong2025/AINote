"""假的 OpenAI 兼容服务端，用来验证流式输出与首轮自动标题。"""
import json
import threading
from http.server import BaseHTTPRequestHandler, HTTPServer


def handle(body):
    msgs = body.get("messages", [])
    sys_text = ""
    for m in msgs:
        if m.get("role") == "system":
            sys_text += m.get("content", "")
    if "标题生成助手" in sys_text:
        return "讨论 Kubernetes 调度"
    return "这是流式回答的第二段。\n这是流式回答的第二段。"


class H(BaseHTTPRequestHandler):
    def log_message(self, *a):
        pass

    def do_POST(self):
        ln = int(self.headers.get("Content-Length", 0))
        body = json.loads(self.rfile.read(ln) or b"{}")
        text = handle(body)
        if body.get("stream"):
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            pieces = [text[:6], text[6:12], text[12:]]
            for i, p in enumerate(pieces):
                d = {"choices": [{"delta": {"content": p}}]}
                self.wfile.write(("data: " + json.dumps(d) + "\n\n").encode())
                self.wfile.flush()
            usage = {"choices": [], "usage": {"prompt_tokens": 123, "completion_tokens": 45}}
            self.wfile.write(("data: " + json.dumps(usage) + "\n\n").encode())
            self.wfile.write(b"data: [DONE]\n\n")
            self.wfile.flush()
        else:
            out = {"choices": [{"message": {"content": text}}]}
            b = json.dumps(out).encode()
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(b)))
            self.end_headers()
            self.wfile.write(b)


if __name__ == "__main__":
    HTTPServer(("127.0.0.1", 5199), H).serve_forever()
