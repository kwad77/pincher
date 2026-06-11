"""Carrier integration stubs."""

from lib.config import get


def create_shipment(order_id, skus):
    carrier = get("default_carrier", "acme-post")
    return f"{carrier}:{order_id}:{len(skus)}"


def cancel_shipment(order_id):
    return f"cancelled:{order_id}"
