import { readFileSync } from 'node:fs';
import vm from 'node:vm';
import assert from 'node:assert/strict';

// A bounded structural check, not a screenshot or live QML validation.
const qml = readFileSync('ResultContent.qml', 'utf8');
assert.match(qml, /readonly property int baseSize:\s*Style\.font\.body\b/);
const context = vm.createContext({});
vm.runInContext(readFileSync('Markdown.js', 'utf8'), context, { timeout: 1000 });
for (const base of [12, 16, 18, 24]) {
    const sizes = Array.from({ length: 6 }, (_, i) =>
        vm.runInContext(`headingSize(${base}, ${i + 1})`, context, { timeout: 1000 }));
    for (let i = 0; i < sizes.length; i++) {
        assert(Number.isInteger(sizes[i]) && sizes[i] > 0, `invalid H${i + 1}`);
        const before = i === 0 ? Math.round(base * 1.6)
            : i === 1 ? Math.round(base * 1.35)
            : i === 2 ? Math.round(base * 1.15) : base + 1;
        assert(sizes[i] <= before, `H${i + 1} unexpectedly increased`);
        if (i < 3) assert(sizes[i] < before, `H${i + 1} did not shrink`);
        if (i > 0) assert(sizes[i - 1] >= sizes[i], `H${i + 1} exceeds H${i}`);
    }
}
console.log('PASS: body uses theme body; headings shrink and remain ordered at four base sizes. Live activation unverified.');
