import test from 'node:test';
import assert from 'node:assert/strict';
import { dependencyGraph } from './ci_dependencies.mjs';

test('imports, re-exports, literal dynamic and require share real dependencies', () => {
  const result = dependencyGraph({'a.ts': `import type {T} from './b'; export {T} from './b';
    const later=import('./b'); const c=require('./b');`, 'b.ts': 'export type T = string;'});
  assert.deepEqual(result, {graph:{'a.ts':['b.ts'],'b.ts':[]},uncertain:[]});
});
test('comments and quoted embedded programs are not module imports', () => {
  const result = dependencyGraph({'a.ts': '// import secret\nconst x=`import sys, json`;'});
  assert.deepEqual(result.uncertain, []);
  assert.deepEqual(result.graph['a.ts'], []);
});
test('computed dependencies and malformed source cannot prove a narrow plan', () => {
  for (const source of ['import(value)', 'require(value)', 'import {'])
    assert.deepEqual(dependencyGraph({'a.ts':source}).uncertain,['a.ts']);
});
test('new URL source readers participate in the graph', () => {
  const result=dependencyGraph({'e/a.ts':`new URL('../s.ts', import.meta.url)`,'s.ts':''});
  assert.deepEqual(result.graph['e/a.ts'],['s.ts']);
});
test('missing dependency including deleted target is uncertain', () => {
  assert.deepEqual(dependencyGraph({'a.ts':`import './gone'`}).uncertain,['a.ts']);
});
test('TSX and import equals are parsed without running modules', () => {
  const result=dependencyGraph({'a.tsx':`import x = require('./b'); const y=<div/>;`,'b.ts':''});
  assert.deepEqual(result.graph['a.tsx'],['b.ts']);
  assert.deepEqual(result.uncertain,[]);
});

test('Vite glob module loading is explicitly uncertain', () => {
  for (const method of ['glob', 'globEager'])
    assert.deepEqual(dependencyGraph({'a.ts':`import.meta.${method}('./*.ts')`}).uncertain,['a.ts']);
});

test('test helper imports remain real edges alongside production consumers', () => {
  const sources = {
    'web/src/Feature.test.tsx': `import {sample} from './sample-data';`,
    'web/src/sample-data.ts': `export {value as sample} from './feature-api';`,
    'web/src/Feature.tsx': `import {value} from './feature-api';`,
    'web/src/feature-api.ts': `export const value = 1;`,
  };
  assert.deepEqual(dependencyGraph(sources), {graph: {
    'web/src/Feature.test.tsx': ['web/src/sample-data.ts'],
    'web/src/sample-data.ts': ['web/src/feature-api.ts'],
    'web/src/Feature.tsx': ['web/src/feature-api.ts'],
    'web/src/feature-api.ts': [],
  }, uncertain: []});
  sources['web/e2e/feature.spec.ts'] = `import '../src/sample-data';`;
  assert.deepEqual(dependencyGraph(sources).graph['web/e2e/feature.spec.ts'], ['web/src/sample-data.ts']);
  sources['web/src/UnknownPage.tsx'] = `import(target);`;
  assert.deepEqual(dependencyGraph(sources).uncertain, ['web/src/UnknownPage.tsx']);
});
