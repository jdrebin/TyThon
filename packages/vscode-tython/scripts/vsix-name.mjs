import assert from "node:assert/strict";

export function validateBuildLabel(label) {
    assert(label === undefined || /^[a-z0-9]+(?:-[a-z0-9]+)*$/i.test(label),
        "--label must contain letters or numbers separated by hyphens, for example embedded-ts.");
}

export function vsixName(version, target, builtAt, label) {
    validateBuildLabel(label);
    const date = new Date(builtAt);
    assert(!Number.isNaN(date.getTime()), "Invalid VSIX build date.");
    const pad = value => String(value).padStart(2, "0");
    const day = [date.getFullYear(), pad(date.getMonth() + 1), pad(date.getDate())].join("-");
    const time = [date.getHours(), date.getMinutes(), date.getSeconds()].map(pad).join("-");
    return ["tython", version, `${day}_${time}`, label, target].filter(value => value !== undefined).join("-") + ".vsix";
}
