const assert = require('node:assert/strict');
const {createRequire} = require('node:module');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const merge = require('deepmerge');

test('deepmerge cannot poison the returned prototype from target or source', () => {
  const poisoned = JSON.parse('{"__proto__":{"hyrulePolluted":true},"nested":{"ok":1}}');
  for (const result of [merge(poisoned, {}), merge(Object.create(null), poisoned)]) {
    assert.equal(Object.getPrototypeOf(result), Object.prototype);
    assert.equal(result.hyrulePolluted, undefined);
    assert.equal({}.hyrulePolluted, undefined);
    assert.equal(Object.hasOwn(result, '__proto__'), true);
    assert.deepEqual(result.__proto__, {hyrulePolluted: true});
  }
  const nested = merge({items: []}, {items: [poisoned]});
  assert.equal(Object.getPrototypeOf(nested.items[0]), Object.prototype);
  assert.equal(nested.items[0].hyrulePolluted, undefined);
});

test('deepmerge preserves consumer merge behavior and copies nested values', () => {
  const requireSvgo = createRequire(require.resolve('@svgr/plugin-svgo'));
  assert.equal(requireSvgo('deepmerge'), merge);
  const symbol = Symbol('config');
  const original = {plugins: ['a'], nested: {a: 1}, [symbol]: {x: 1}};
  const date = new Date('2026-01-01T00:00:00Z');
  const result = merge.all([original, {plugins: ['b'], nested: {b: 2}, date}]);
  assert.deepEqual(result.plugins, ['a', 'b']);
  assert.deepEqual(result.nested, {a: 1, b: 2});
  result.nested.a = 3;
  result[symbol].x = 2;
  assert.equal(original.nested.a, 1);
  assert.equal(original[symbol].x, 1);
  assert.equal(result.date, date);
  assert.deepEqual(merge({a: [1]}, {a: [2]}, {arrayMerge: (_, source) => source}), {a: [2]});
  assert.deepEqual(merge({a: {}}, {a: {}}, {customMerge: () => () => 3}), {a: 3});
  assert.equal(Object.getPrototypeOf(merge({}, JSON.parse('{"__proto__":{"x":1}}'))), Object.prototype);
});

test('webpack consumers use the corrected serializer and escape script endings', () => {
  for (const consumer of ['copy-webpack-plugin', 'css-minimizer-webpack-plugin']) {
    const requireConsumer = createRequire(require.resolve(consumer));
    const serialize = requireConsumer('serialize-javascript');
    const encoded = serialize({text: '</script><script>alert(1)</script>', regexp: /a+/gi, date: new Date(0)});
    assert.equal(encoded.includes('</script'), false);
    const decoded = vm.runInNewContext(`(${encoded})`);
    assert.equal(decoded.text, '</script><script>alert(1)</script>');
    assert.equal(decoded.regexp.source, 'a+');
    assert.equal(decoded.regexp.flags, 'gi');
    assert.equal(decoded.date.getTime(), 0);
    const encodedFunction = serialize(function markup() { return '</script>'; });
    assert.equal(encodedFunction.toLowerCase().includes('</script'), false);
    assert.equal(vm.runInNewContext(`(${encodedFunction})`)(), '</script>');
    const trailingComment = function marker() {};
    trailingComment.toString = () => 'function marker(){}//</script';
    assert.equal(serialize(trailingComment).toLowerCase().includes('</script'), false);
    const spoofedDate = new Date(0);
    spoofedDate.toISOString = () => '\");globalThis.hyruleInjected=true;//';
    assert.throws(() => serialize(spoofedDate), TypeError);
    const spoofedRegexp = /safe/;
    Object.defineProperty(spoofedRegexp, 'flags', {value: '\");globalThis.hyruleInjected=true;//'});
    assert.equal(serialize(spoofedRegexp).includes('hyruleInjected'), false);
  }
});

test('sockjs remains compatible with uuid v4 and rejects invalid v5 buffers', () => {
  const requireSockjs = createRequire(require.resolve('sockjs'));
  const uuid = requireSockjs('uuid');
  assert.match(uuid.v4(), /^[\da-f]{8}-[\da-f]{4}-4[\da-f]{3}-[89ab][\da-f]{3}-[\da-f]{12}$/);
  assert.throws(() => uuid.v5('hyrule', uuid.v5.DNS, new Uint8Array(8), 4), RangeError);
});

test('Docusaurus worker pool ignores an inherited run filename', async () => {
  const requireDocusaurus = createRequire(require.resolve('@docusaurus/core/package.json'));
  const {default: Tinypool} = await import(requireDocusaurus.resolve('tinypool'));
  const pool = new Tinypool({filename: path.join(__dirname, 'security-worker.cjs'), minThreads: 1, maxThreads: 1});
  try {
    const options = Object.create({filename: path.join(__dirname, 'nonexistent-worker.cjs')});
    assert.deepEqual(await pool.run({n: 42}, options), {n: 42});
  } finally {
    await pool.destroy();
  }
});
