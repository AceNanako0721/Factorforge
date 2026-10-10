// Compiler-AST check for this registered TypeScript module. No private files read.
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
const root = path.resolve(import.meta.dirname, "..");
const base = path.join(root, "src/factorforge/applications/model_access");
const require = createRequire(path.join(base, "package.json")),
  ts = require("typescript");
const failures = [];
function walk(dir) {
  for (const e of fs.readdirSync(dir, { withFileTypes: true })) {
    if (["node_modules"].includes(e.name)) continue;
    const file = path.join(dir, e.name);
    if (e.isDirectory()) {
      walk(file);
      continue;
    }
    if (!/\.(ts|mjs)$/.test(e.name)) continue;
    const relative = path.relative(base, file);
    if (!/^(api|config|adapters|application|entrypoints)[/\\]/.test(relative))
      failures.push(relative + ": source outside registered area");
    const source = ts.createSourceFile(
      file,
      fs.readFileSync(file, "utf8"),
      ts.ScriptTarget.Latest,
      true,
    );
    function check(spec) {
      if (spec.startsWith(".")) {
        const resolved = path.resolve(path.dirname(file), spec);
        if (!resolved.startsWith(base + path.sep))
          failures.push(relative + ": cross-module private import");
      } else if (
        !spec.startsWith("node:") &&
        !["openai", "jose", "smol-toml", "string-width"].includes(spec)
      )
        failures.push(relative + ": unregistered dependency " + spec);
    }
    function visit(n) {
      if (ts.isImportDeclaration(n) || ts.isExportDeclaration(n)) {
        if (n.moduleSpecifier) {
          if (ts.isStringLiteral(n.moduleSpecifier))
            check(n.moduleSpecifier.text);
          else failures.push(relative + ": nonliteral import");
        }
      }
      if (
        ts.isCallExpression(n) &&
        (n.expression.kind === ts.SyntaxKind.ImportKeyword ||
          (ts.isIdentifier(n.expression) && n.expression.text === "require"))
      )
        failures.push(relative + ": dynamic module access");
      ts.forEachChild(n, visit);
    }
    visit(source);
  }
}
walk(base);
if (failures.length) {
  console.error(failures.join("\n"));
  process.exitCode = 1;
} else
  console.log(
    "OK: model access TypeScript placement and static import boundaries",
  );
