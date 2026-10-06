// One row per machine we ship. The Go compiler cross-compiles.
// The frozen formatter does not: it is bundled only when this row is the host.
export const targets = {
    "linux-x64": { goos: "linux", goarch: "amd64", vsce: "linux-x64", binary: "tython", formatter: "black-formatter", wheel: "linux_x86_64" },
    "linux-arm64": { goos: "linux", goarch: "arm64", vsce: "linux-arm64", binary: "tython", formatter: "black-formatter", wheel: "linux_aarch64" },
    "darwin-x64": { goos: "darwin", goarch: "amd64", vsce: "darwin-x64", binary: "tython", formatter: "black-formatter", wheel: "macosx_10_15_x86_64" },
    "darwin-arm64": { goos: "darwin", goarch: "arm64", vsce: "darwin-arm64", binary: "tython", formatter: "black-formatter", wheel: "macosx_11_0_arm64" },
    "win32-x64": { goos: "windows", goarch: "amd64", vsce: "win32-x64", binary: "tython.exe", formatter: "black-formatter.exe", wheel: "win_amd64" },
};

export function hostTargetName() {
    const goos = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
    const goarch = { x64: "amd64", arm64: "arm64" }[process.arch];
    const found = Object.entries(targets).find(([, target]) => target.goos === goos && target.goarch === goarch);
    if (!found) throw new Error(`No tython package target for ${process.platform}/${process.arch}`);
    return found[0];
}

export function targetByName(name) {
    const target = targets[name];
    if (!target) throw new Error(`Unknown target ${name}. Known: ${Object.keys(targets).join(", ")}`);
    return { name, ...target, compilerFile: `bin/${target.binary}` };
}

export function canRun(target) {
    const goos = { linux: "linux", darwin: "darwin", win32: "windows" }[process.platform];
    const goarch = { x64: "amd64", arm64: "arm64" }[process.arch];
    return target.goos === goos && target.goarch === goarch;
}
