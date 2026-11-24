import { readFileSync } from 'node:fs';
import * as c4 from 'c4-sdk/core';
const raw = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const r = c4.decodeArchitecture(raw);
const diags = r.diagnostics ?? [];
console.log('decode ok:', r.ok, '| errors:', diags.filter(d => d.severity === 'error').length, '| warnings:', diags.filter(d => d.severity === 'warning').length);
for (const d of diags.slice(0, 20)) console.log(' ', d.severity, d.code, d.path, d.message);
if (!r.ok) process.exit(1);

const arch = r.value.index ?? r.value;
let okN = 0; const bad = [];
for (const d of raw.diagrams) {
  try { const s = c4.buildScene(arch, d.id); const n = (s.nodes ?? s.scene?.nodes ?? []).length; if (s && s.ok !== false) okN++; else bad.push([d.id, JSON.stringify(s).slice(0, 200)]); }
  catch (e) { bad.push([d.id, String(e).slice(0, 200)]); }
}
console.log('scenes built:', okN, 'of', raw.diagrams.length, bad.length ? JSON.stringify(bad) : '');
process.exit(bad.length ? 1 : 0);
