# Pyright Type Server Protocol

`pyrightTypeServerProtocol.ts` is copied without modification from
[microsoft/pyright 1.1.414](https://github.com/microsoft/pyright/blob/1.1.414/packages/pyright-internal/src/typeServer/protocol/typeServerProtocol.ts).
Its Microsoft copyright and MIT license notice are preserved. The associated
MIT license is distributed with the pinned `pyright-typeserver` dependency.

Update this file and the dependency together. The adapter negotiates protocol
version before using requests; it does not import Pyright's private internals.
