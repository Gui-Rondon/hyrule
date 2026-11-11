// A single dark forest/charcoal Prism theme, used for both page color
// modes — per the Kokiri Forest redesign brief, code blocks stay dark
// regardless of whether the surrounding page is light or dark.
//
// Colors come from the --hy-* palette in src/css/custom.css (not read as
// CSS variables here: prism-react-renderer themes are plain JS objects
// consumed at render time, so the values are duplicated as literals).

/** @type {import('prism-react-renderer').PrismTheme} */
const hyruleCodeTheme = {
  plain: {
    color: '#d5c69f',
    backgroundColor: '#192c24',
  },
  styles: [
    {
      types: ['comment', 'prolog', 'doctype', 'cdata'],
      style: {color: '#93a17d', fontStyle: 'italic'},
    },
    {
      types: ['punctuation'],
      style: {color: '#9c8f6f'},
    },
    {
      types: ['property', 'tag', 'boolean', 'number', 'constant', 'symbol', 'deleted'],
      style: {color: '#a8b897'},
    },
    {
      types: ['selector', 'attr-name', 'string', 'char', 'builtin', 'inserted'],
      style: {color: '#cdae82'},
    },
    {
      types: ['operator', 'entity', 'url', 'variable'],
      style: {color: '#8fb3ad'},
    },
    {
      types: ['atrule', 'attr-value', 'function', 'class-name'],
      style: {color: '#e0b97a'},
    },
    {
      types: ['keyword'],
      style: {color: '#8fb3ad', fontStyle: 'italic'},
    },
    {
      types: ['regex', 'important'],
      style: {color: '#cdae82'},
    },
    {
      types: ['important', 'bold'],
      style: {fontWeight: 'bold'},
    },
    {
      types: ['italic'],
      style: {fontStyle: 'italic'},
    },
  ],
};

export default hyruleCodeTheme;
