def load_user(identifier):
    return User(identifier, "Ada")

class User:
    kind = "user"

    def __init__(self, identifier, name):
        self.identifier = identifier
        self.name = name
        self.tags = ["compiler", "demo"]

    @property
    def label(self):
        return f"{self.identifier}: {self.name}"

    @load_user
    def rename(self, name):
        self.name = name
        return self

class Test:
    if True == False:
        name: str

fg = { ("hello"): 23 }
print(fg["hello"])