def lookup(key):
    return {"name": "Ada", "count": 3}[key]


class Greeting:
    def __init__(self, name):
        self.name = name

    def render(self, *, prefix="Hello"):
        return f"{prefix}, {self.name}"
