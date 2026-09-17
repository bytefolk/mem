# Shared design token bridge

`tokens.json` is an unmodified snapshot of
[`bytefolk/design-system` at 910456901dda74da4d5b0320cd03d36ad18650b0](https://github.com/bytefolk/design-system/blob/910456901dda74da4d5b0320cd03d36ad18650b0/tokens/design-tokens.json).
The upstream Apache-2.0 license is included as `LICENSE`.

The shared JSON owns the palette. Do not edit the snapshot or generated CSS by
hand. To update it, copy the JSON from a reviewed upstream commit, update the
revision and expected SHA-256 in `../scripts/design-tokens.mjs`, regenerate, and review both themes.
The CSS header records the commit and content SHA-256.

```sh
cd web
npm run tokens:generate
npm run tokens:check
```

`tokens:check` runs before every Web build. The zero-dependency generator maps
the default profile to mem's existing RGB variable API, preserves alpha, and
uses shared font/shadow roles. Human actions are blue; `ai` is reserved for AI
affordances. The user's saved light/dark preference is retained.

Upstream base hues include decorative contrast exceptions. For actual small
text, the adapter raises source alpha or adjusts an opaque source hue toward
the theme's foreground until it reaches 4.5:1 on all five source surfaces.
Semantic foregrounds also meet this ratio on up to 30% tinted state backgrounds. Solid normal/hover backgrounds
are derived from upstream primary/hover hues and paired with the upstream
primary foreground. Generation checks 334 text/background pairs; actual
browser acceptance additionally checks compositing and control states.

mem currently uses React 19 and local primitives; the published shared facade
expects React 18. This bridge deliberately adds no runtime dependency or React
migration. The local `EmptyState` follows the shared `ui-empty-state` structure
and spacing; it can be replaced by a compatible shared release later. Page,
card, dialog and form content starts at the reading edge; numeric comparison
columns and trailing actions align to the end; buttons, badges, tabs and whole
empty panels center their content. Business routes and actions stay in mem.
