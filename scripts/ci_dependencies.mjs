// Uses the repository's locked TypeScript parser, never executes imported source.
import ts from '../web/node_modules/typescript/lib/typescript.js';
import path from 'node:path';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';

export function dependencyGraph(sources) {
  const graph = {}, uncertain = [];
  for (const [name, text] of Object.entries(sources)) {
    const edges = new Set();
    let unknown = false;
    const local = (value) => {
      if (!value.startsWith('.')) {
        if (value.startsWith('@/') || value.startsWith('~/')) unknown = true;
        return;
      }
      const stem = path.posix.normalize(path.posix.join(path.posix.dirname(name), value));
      const targets = [stem, `${stem}.ts`, `${stem}.tsx`, `${stem}/index.ts`, `${stem}/index.tsx`]
        .filter(p => Object.hasOwn(sources, p));
      if (targets.length !== 1) unknown = true;
      else edges.add(targets[0]);
    };
    if (name.endsWith('.css')) {
      // CSS imports need their own parser; uncertain CSS consumers widen safely.
      unknown = /@import\b/.test(text);
    } else {
      const file = ts.createSourceFile(name, text, ts.ScriptTarget.Latest, true,
        name.endsWith('.tsx') ? ts.ScriptKind.TSX : ts.ScriptKind.TS);
      if (file.parseDiagnostics.length) unknown = true;
      const literal = node => node && (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node));
      const visit = node => {
        if ((ts.isImportDeclaration(node) || ts.isExportDeclaration(node)) && node.moduleSpecifier) {
          if (literal(node.moduleSpecifier)) local(node.moduleSpecifier.text);
          else unknown = true;
        }
        if (ts.isImportEqualsDeclaration(node)) {
          if (ts.isExternalModuleReference(node.moduleReference) && literal(node.moduleReference.expression))
            local(node.moduleReference.expression.text);
          else unknown = true;
        }
        if (ts.isCallExpression(node) && (node.expression.kind === ts.SyntaxKind.ImportKeyword ||
            (ts.isIdentifier(node.expression) && node.expression.text === 'require'))) {
          if (node.arguments.length === 1 && literal(node.arguments[0])) local(node.arguments[0].text);
          else unknown = true;
        }
        if (ts.isNewExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === 'URL' &&
            node.arguments?.length === 2 && node.arguments[1].getText(file) === 'import.meta.url') {
          if (literal(node.arguments[0])) local(node.arguments[0].text);
          else unknown = true;
        }
        if (ts.isCallExpression(node) && ((ts.isIdentifier(node.expression) &&
            ['eval', 'Function'].includes(node.expression.text)) ||
            (ts.isPropertyAccessExpression(node.expression) && ts.isIdentifier(node.expression.expression) &&
             node.expression.expression.text === 'require'))) unknown = true;
        if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) &&
            ['glob', 'globEager'].includes(node.expression.name.text) &&
            node.expression.expression.getText(file) === 'import.meta') unknown = true;
        ts.forEachChild(node, visit);
      };
      visit(file);
    }
    graph[name] = [...edges].sort();
    if (unknown) uncertain.push(name);
  }
  return {graph, uncertain: uncertain.sort()};
}
if (process.argv[1] && fileURLToPath(import.meta.url) === path.resolve(process.argv[1])) {
  process.stdout.write(JSON.stringify(dependencyGraph(JSON.parse(readFileSync(0, 'utf8')))));
}
