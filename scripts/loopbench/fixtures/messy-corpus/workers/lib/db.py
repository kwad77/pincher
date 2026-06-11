"""Worker-side persistence stubs."""

_STOCK = {}
_FULFILLED = {}


def reserve_stock(sku):
    _STOCK[sku] = _STOCK.get(sku, 100) - 1


def release_stock(order_id):
    _FULFILLED.pop(order_id, None)


def set_stock_level(sku, count):
    _STOCK[sku] = count


def mark_fulfilled(order_id, shipment):
    _FULFILLED[order_id] = shipment


def charge_card_legacy(order_id, total_cents):
    raise RuntimeError("worker-side capture was removed in the billing split")
