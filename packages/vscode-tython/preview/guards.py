"""Runtime implementations for guards.d.ty; declarations do not validate data."""


def is_text(value: str | int) -> bool:
    return isinstance(value, str)


def require_text(value: str | int) -> None:
    if not isinstance(value, str):
        raise TypeError("Expected text")
