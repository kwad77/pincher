"""Claw back fulfillment after billing has refunded payment."""

from lib import db, notify, shipping
from lib.registry import register


@register("process_refund")
def process_refund(job):
    order_id = job["order_id"]
    shipping.cancel_shipment(order_id)
    db.release_stock(order_id)
    notify.send_email(job.get("email", ""), "refunded", job.get("reason", ""))
