#!/usr/bin/env node
// scripts/validate-mcp-schemas.js
// Validates MCP schema files for developer, qa, and reviewer roles.
// Requirements from Issue #16:
//   1. All files are valid JSON.
//   2. qa_tools.json has test.run with risk_level "R1".
//   3. Every file has a deny list containing "deploy.prod".

const fs = require('fs');
const path = require('path');

const SCHEMAS_DIR = path.resolve(__dirname, '..', 'packages', 'contracts', 'mcp-schemas');
const FILES = {
  developer: 'developer_tools.json',
  qa: 'qa_tools.json',
  reviewer: 'reviewer_tools.json',
};

let passed = 0;
let failed = 0;

function pass(msg) {
  console.log(`PASS: ${msg}`);
  passed++;
}

function fail(msg) {
  console.log(`FAIL: ${msg}`);
  failed++;
}

// ── Check 1: JSON validity ──────────────────────────────────────
console.log('--- Check 1: JSON validity ---');
for (const [role, file] of Object.entries(FILES)) {
  const filePath = path.join(SCHEMAS_DIR, file);
  try {
    const raw = fs.readFileSync(filePath, 'utf8');
    if (!raw.trim()) {
      fail(`[${role}] empty file`);
      continue;
    }
    JSON.parse(raw);
    pass(`[${role}] valid JSON`);
  } catch (err) {
    fail(`[${role}] ${err.message}`);
  }
}
console.log();

// ── Check 2: qa test.run = R1 ───────────────────────────────────
console.log('--- Check 2: qa test.run = R1 ---');
try {
  const qaPath = path.join(SCHEMAS_DIR, 'qa_tools.json');
  const qaData = JSON.parse(fs.readFileSync(qaPath, 'utf8'));
  const testRun = qaData.tools?.find((t) => t.name === 'test.run');
  if (testRun && testRun.risk_level === 'R1') {
    pass('qa_tools.json test.run has risk_level R1');
  } else {
    fail(`qa_tools.json test.run risk_level is "${testRun?.risk_level}" (expected "R1")`);
  }
} catch (err) {
  fail(`Cannot validate qa test.run: ${err.message}`);
}
console.log();

// ── Check 3: deny list contains deploy.prod ─────────────────────
console.log('--- Check 3: deny list contains deploy.prod ---');
for (const [role, file] of Object.entries(FILES)) {
  const filePath = path.join(SCHEMAS_DIR, file);
  try {
    const data = JSON.parse(fs.readFileSync(filePath, 'utf8'));
    const deny = data.deny;
    if (Array.isArray(deny) && deny.includes('deploy.prod')) {
      pass(`[${role}] deny list contains deploy.prod`);
    } else {
      fail(`[${role}] deny list missing deploy.prod (has: ${JSON.stringify(deny)})`);
    }
  } catch (err) {
    fail(`[${role}] cannot read: ${err.message}`);
  }
}
console.log();

// ── Summary ─────────────────────────────────────────────────────
console.log('=== Summary ===');
console.log(`Passed: ${passed}`);
console.log(`Failed: ${failed}`);
if (failed > 0) {
  console.log('RESULT: FAIL');
  process.exit(1);
}
console.log('RESULT: ALL CHECKS PASSED');
