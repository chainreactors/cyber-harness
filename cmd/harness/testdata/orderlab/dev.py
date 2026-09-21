"""Local development lifecycle and HTTP client. No model responses or verdicts."""
import hashlib
import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import time
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parent
CONFIG = json.loads((ROOT / ".dev.json").read_text(encoding="utf-8"))
STATE = ROOT / ".process.json"


def revision():
    h = hashlib.sha256()
    for name in ("main.go", "policy.go"):
        h.update(name.encode())
        h.update((ROOT / name).read_bytes())
    return h.hexdigest()


def call(method, path, alias="none", data=None):
    if not path.startswith("/") or path.startswith("//"):
        raise ValueError("path must be local and absolute")
    headers = {}
    if alias != "none":
        seed = json.loads((ROOT / "seed.json").read_text(encoding="utf-8"))
        user = next(u for u in seed["Users"] if u["ID"] == alias)
        headers["Authorization"] = "Bearer " + user["Token"]
    payload = None if data is None else data.encode()
    if payload is not None:
        headers["Content-Type"] = "application/json"
    request = urllib.request.Request(CONFIG["url"] + path, data=payload, headers=headers, method=method)
    try:
        response = urllib.request.urlopen(request, timeout=10)
    except urllib.error.HTTPError as exc:
        response = exc
    with response:
        raw = response.read()
        return {"status": response.status, "request_id": response.headers.get("X-Request-ID"),
                "revision": response.headers.get("X-Revision"), "retry_after": response.headers.get("Retry-After"),
                "body_sha256": hashlib.sha256(raw).hexdigest(), "body": json.loads(raw)}


def stop():
    if not STATE.exists():
        return
    state = json.loads(STATE.read_text(encoding="utf-8"))
    pid = state["pid"]
    expected = Path(state["exe"]).resolve()
    if expected.parent != ROOT or not expected.name.startswith("orderlab-"):
        raise RuntimeError("unexpected process executable")
    if os.name == "nt":
        import ctypes
        from ctypes import wintypes
        kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        kernel.OpenProcess.argtypes = (wintypes.DWORD, wintypes.BOOL, wintypes.DWORD)
        kernel.OpenProcess.restype = wintypes.HANDLE
        kernel.QueryFullProcessImageNameW.argtypes = (wintypes.HANDLE, wintypes.DWORD, wintypes.LPWSTR, ctypes.POINTER(wintypes.DWORD))
        kernel.TerminateProcess.argtypes = (wintypes.HANDLE, wintypes.UINT)
        kernel.WaitForSingleObject.argtypes = (wintypes.HANDLE, wintypes.DWORD)
        kernel.CloseHandle.argtypes = (wintypes.HANDLE,)
        handle = kernel.OpenProcess(0x1000 | 0x0001 | 0x00100000, False, pid)
        if handle:
            try:
                buf = ctypes.create_unicode_buffer(32768)
                size = wintypes.DWORD(len(buf))
                if not kernel.QueryFullProcessImageNameW(handle, 0, buf, ctypes.byref(size)):
                    raise RuntimeError("cannot verify target process")
                if Path(buf.value).resolve() != expected:
                    raise RuntimeError("PID now belongs to another process")
                if not kernel.TerminateProcess(handle, 0):
                    raise RuntimeError("cannot stop target")
                if kernel.WaitForSingleObject(handle, 5000) != 0:
                    raise RuntimeError("target did not exit")
            finally:
                kernel.CloseHandle(handle)
    else:
        actual = Path(f"/proc/{pid}/exe")
        if actual.exists():
            if actual.resolve() != expected:
                raise RuntimeError("PID now belongs to another process")
            os.kill(pid, signal.SIGINT)
            for _ in range(50):
                if not actual.exists():
                    break
                time.sleep(0.1)
            else:
                raise RuntimeError("target did not exit")
    STATE.unlink(missing_ok=True)


def start():
    digest = revision()
    exe = ROOT / ("orderlab-" + digest[:12] + (".exe" if os.name == "nt" else ""))
    subprocess.run(["go", "build", "-mod=mod", "-o", str(exe), "."], cwd=ROOT, check=True, timeout=90)
    stop()
    cmd = [str(exe), "--addr", CONFIG["addr"], "--db", str(ROOT / "orders.db"),
           "--seed", str(ROOT / "seed.json"), "--audit", CONFIG["audit"], "--revision", digest]
    with open(ROOT / "service.log", "ab", buffering=0) as output:
        proc = subprocess.Popen(cmd, cwd=ROOT, stdin=subprocess.DEVNULL, stdout=output, stderr=output,
                                creationflags=0x08000000 if os.name == "nt" else 0,
                                start_new_session=os.name != "nt")
    STATE.write_text(json.dumps({"pid": proc.pid, "exe": str(exe), "revision": digest}), encoding="utf-8")
    for _ in range(100):
        if proc.poll() is not None:
            raise RuntimeError("target exited during startup; inspect service.log")
        try:
            result = call("GET", "/health")
            if result["status"] == 200 and result["revision"] == digest:
                return {"url": CONFIG["url"], "revision": digest, "pid": proc.pid}
        except (OSError, ValueError):
            pass
        time.sleep(0.1)
    raise RuntimeError("target did not become ready")


if __name__ == "__main__":
    action = sys.argv[1]
    if action in ("start", "restart"):
        result = start()
    elif action == "stop":
        stop()
        result = {"stopped": True}
    elif action == "status":
        result = call("GET", "/version")
    elif action == "request":
        result = call(sys.argv[2], sys.argv[3], sys.argv[4] if len(sys.argv) > 4 else "none", sys.argv[5] if len(sys.argv) > 5 else None)
    else:
        raise ValueError("use start, restart, stop, status or request METHOD PATH [USER_ID] [JSON]")
    print(json.dumps(result, ensure_ascii=False))
