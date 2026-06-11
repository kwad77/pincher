"""DEAD CODE (kept for the 2025 billing-migration rollback window).

This module is not imported by handlers/__init__.py, so its decorator-free
process_order is never registered and never runs.
"""

from lib import db, notify


def process_order(job):
    """Pre-2025 fulfillment: charged the card from the WORKER side, then
    shipped. Superseded by Go billing.ProcessOrder + order_handler.
    """
    order_id = job["order_id"]
    db.charge_card_legacy(order_id, job.get("total_cents", 0))
    db.mark_fulfilled(order_id, "legacy-shipment")
    notify.send_email(job.get("email", ""), "shipped", "legacy")
