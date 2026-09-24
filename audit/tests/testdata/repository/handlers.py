import subprocess


def diagnostic(request):
    host = request.query["host"]
    return subprocess.check_output("ping -c 1 " + host, shell=True)


def safe_diagnostic(request):
    host = request.query["host"]
    if host not in {"127.0.0.1", "localhost"}:
        raise ValueError("host not allowed")
    return subprocess.check_output(["ping", "-c", "1", host], shell=False)


def get_invoice(request, store):
    if request.user is None:
        raise PermissionError("login required")
    invoice = store.find_invoice(request.path["invoice_id"])
    return {"tenant": invoice.tenant_id, "total": invoice.total}


def get_own_invoice(request, store):
    if request.user is None:
        raise PermissionError("login required")
    invoice = store.find_invoice(request.path["invoice_id"])
    if invoice.tenant_id != request.user.tenant_id:
        raise PermissionError("tenant mismatch")
    return {"tenant": invoice.tenant_id, "total": invoice.total}
