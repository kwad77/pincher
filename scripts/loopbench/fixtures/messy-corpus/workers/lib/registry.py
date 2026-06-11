"""Job-type -> handler registry. Populated by @register decorators."""

HANDLERS = {}


def register(job_type):
    """Bind a job_type string to the decorated handler function."""

    def deco(fn):
        HANDLERS[job_type] = fn
        return fn

    return deco
