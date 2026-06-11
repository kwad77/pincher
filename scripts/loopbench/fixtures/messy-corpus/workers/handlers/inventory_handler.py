"""Nightly inventory reconciliation against the warehouse export."""

from lib import db
from lib.registry import register


@register("inventory.sync")
def sync_inventory(job):
    for sku, count in job.get("counts", {}).items():
        db.set_stock_level(sku, count)
