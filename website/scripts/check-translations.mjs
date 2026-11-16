import {readdir, readFile} from 'node:fs/promises';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const website = fileURLToPath(new URL('../', import.meta.url));
const translated = path.join(website, 'i18n/pt-BR/docusaurus-plugin-content-docs/current');

async function markdownFiles(dir, prefix = '') {
  const files = [];
  for (const entry of await readdir(dir, {withFileTypes: true})) {
    const name = path.join(prefix, entry.name);
    if (entry.isDirectory()) files.push(...await markdownFiles(path.join(dir, entry.name), name));
    else if (/\.mdx?$/.test(entry.name)) files.push(name);
  }
  return files.sort();
}

// Docusaurus silently falls back to English for missing localized docs. Make
// that omission fail the build, rather than publishing a mixed-language site.
const sourceFiles = await markdownFiles(path.join(website, 'docs'));
const translatedFiles = new Set(await markdownFiles(translated));
const errors = [];
for (const name of sourceFiles) {
  if (!translatedFiles.has(name)) {
    errors.push(`Missing pt-BR document: ${name}`);
    continue;
  }
  const source = await readFile(path.join(website, 'docs', name), 'utf8');
  const target = await readFile(path.join(translated, name), 'utf8');
  if (!target.trim() || source.trim() === target.trim()) {
    errors.push(`Empty or unchanged English document in pt-BR: ${name}`);
  }
}
for (const name of translatedFiles) {
  if (!sourceFiles.includes(name)) errors.push(`pt-BR document has no English counterpart: ${name}`);
}

const code = JSON.parse(await readFile(path.join(website, 'i18n/pt-BR/code.json'), 'utf8'));
const home = await readFile(path.join(website, 'src/pages/index.js'), 'utf8');
const ids = [...home.matchAll(/\bid(?:=|:)\s*['"]([^'"]+)['"]/g)].map((match) => match[1]);
for (const id of ids) {
  if (!code[id]?.message?.trim()) errors.push(`Missing pt-BR homepage message: ${id}`);
}

if (errors.length) {
  console.error(errors.join('\n'));
  process.exitCode = 1;
} else {
  console.log(`pt-BR: ${sourceFiles.length} documents and ${ids.length} homepage messages checked.`);
}
