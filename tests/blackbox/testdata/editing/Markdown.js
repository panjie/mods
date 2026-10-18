// Synthetic formatter; evaluated by check.mjs, not a live QML renderer.
function headingSize(base, level) {
    return level === 1 ? Math.round(base * 1.6)
        : level === 2 ? Math.round(base * 1.35)
        : level === 3 ? Math.round(base * 1.15)
        : base + 1;
}
