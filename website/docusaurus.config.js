// @ts-check
import {execSync} from 'node:child_process';
import {themes as prismThemes} from 'prism-react-renderer';

// The nearest release tag (e.g. "v0.100.0"), or null off a shallow clone
// with no tags (the local working copy still builds, just without the
// footer link) — see deploy-docs.yml's checkout step for why CI has them.
function latestVersion() {
  try {
    return execSync('git describe --tags --abbrev=0', {stdio: ['ignore', 'pipe', 'ignore']})
      .toString()
      .trim();
  } catch {
    return null;
  }
}

const version = latestVersion();

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'hyrule',
  tagline: 'Spin up microservices fast. Test their flows just as fast.',
  favicon: 'img/logo.png',

  future: {
    v4: true,
  },

  url: 'https://gui-rondon.github.io',
  baseUrl: '/hyrule/',

  organizationName: 'Gui-Rondon',
  projectName: 'hyrule',

  onBrokenLinks: 'throw',
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en', 'pt-BR'],
    localeConfigs: {
      en: {label: 'English'},
      'pt-BR': {label: 'Português'},
    },
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          path: 'docs',
          routeBasePath: 'docs',
          sidebarPath: './sidebars.js',
          editUrl: 'https://github.com/Gui-Rondon/hyrule/tree/main/website/',
        },
        blog: false,
        theme: {
          customCss: './src/css/custom.css',
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      image: 'img/logo.png',
      // false (the default) keeps the navbar toggle a 2-state light/dark
      // switch; true adds a third "system" state to the same button.
      colorMode: {
        respectPrefersColorScheme: false,
      },
      navbar: {
        title: 'hyrule',
        logo: {
          alt: 'hyrule logo',
          src: 'img/logo.png',
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'learnSidebar',
            position: 'left',
            label: 'Learn',
          },
          {
            type: 'docSidebar',
            sidebarId: 'referenceSidebar',
            position: 'left',
            label: 'Reference',
          },
          {
            type: 'localeDropdown',
            position: 'right',
          },
          {
            href: 'https://github.com/Gui-Rondon/hyrule',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Docs',
            items: [
              {label: 'Learn', to: '/docs/learn/intro'},
              {label: 'Reference', to: '/docs/reference/architecture'},
            ],
          },
          {
            title: 'More',
            items: [
              {label: 'GitHub', href: 'https://github.com/Gui-Rondon/hyrule'},
              {label: 'blog-apis example', href: 'https://github.com/Gui-Rondon/hyrule/tree/main/examples/blog-apis'},
              ...(version
                ? [{label: version, href: `https://github.com/Gui-Rondon/hyrule/releases/tag/${version}`}]
                : []),
            ],
          },
          {
            title: 'About',
            items: [
              {label: 'About Me', href: 'https://guirondon.dev/about/'},
              {label: 'LinkedIn', href: 'https://www.linkedin.com/in/guilherme-rondon/'},
            ],
          },
          {
            title: 'Get in Touch',
            items: [
              {label: 'Contact', href: 'https://guirondon.dev/contact/'},
              {label: 'Help me Building', href: 'https://invite.guirondon.dev/'},
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Guilherme Rondon. MIT licensed.`,
      },
      prism: {
        theme: prismThemes.github,
        darkTheme: prismThemes.dracula,
        additionalLanguages: ['go', 'yaml', 'bash', 'json'],
      },
    }),
};

export default config;
