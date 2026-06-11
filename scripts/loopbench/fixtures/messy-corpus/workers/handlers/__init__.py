"""Importing this package registers every LIVE handler via decorators.

legacy.py is deliberately NOT imported: its process_order predates the
billing split and is kept only as a reference while the rollback window
is open.
"""

from . import inventory_handler  # noqa: F401
from . import order_handler  # noqa: F401
from . import refund_handler  # noqa: F401
