# Temporary deepmerge security fork

This private local package is **deepmerge 4.3.1 with a security patch**, not an
upstream release. `website/package.json` substitutes it for every `deepmerge`
consumer. Keep the MIT license alongside the source. No install-time patching
or downloads are required.

Upstream: https://github.com/TehShrike/deepmerge/tree/v4.3.1

Source: `dist/cjs.js` from the npm `deepmerge@4.3.1` tarball. Its unmodified
SHA-256 is `2e5160f84a1141bd927264afe4d15766f1e6cf07daef182b37ff53fea121de7d`.
The readable distribution includes `is-mergeable-object`, just as upstream does.

Advisory: https://github.com/advisories/GHSA-gcqx-w48m-w2h4

## Patch

The three writes in `mergeObject` now use `Object.defineProperty` to create own,
enumerable, writable data properties. In particular, cloning an own `__proto__`
key from a target, or merging one from a source into a null-prototype target,
must not call the legacy prototype setter on the returned object. The existing
checks against merging inherited/nonenumerable target properties remain intact.
Array concatenation, symbols, custom merge options and special-object behavior
are preserved. The replacement has an explicit private fork identity so it is
not represented as a nonexistent patched upstream version.

Run `npm run test:security` from `website` for the exploit regressions and
compatibility tests. Run `npm ci`, `npm audit` and the EN/PT build before updates.
Review upstream at each dependency update; remove this fork and its override
only after a compatible release fixes the advisory and passes those tests.
