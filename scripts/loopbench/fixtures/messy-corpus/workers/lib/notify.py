"""Outbound customer email (stub)."""


def send_email(to, template, detail):
    if not to:
        return
    print(f"email to={to} template={template} detail={detail}")
