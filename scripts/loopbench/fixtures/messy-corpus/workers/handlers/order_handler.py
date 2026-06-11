"""Fulfillment side of an order: reserve stock, ship, notify."""

from lib import db, notify, shipping
from lib.registry import register


@register("process_order")
def process_order(job):
    """Fulfill one paid order (job published by Go orders.ProcessOrder).

    Same name as the Go pipeline entrypoint and the TS client helper, but
    this one does FULFILLMENT: inventory, shipment, customer email.
    """
    order_id = job["order_id"]
    for sku in job.get("skus", []):
        db.reserve_stock(sku)
    shipment = shipping.create_shipment(order_id, job.get("skus", []))
    db.mark_fulfilled(order_id, shipment)
    notify.send_email(job.get("email", ""), "shipped", shipment)
