// @ts-check
import {themes as prismThemes} from 'prism-react-renderer';

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'hyrule',
  tagline: 'A Go framework for building REST APIs on MongoDB',
  favicon: 'img/favicon.ico',

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
      image: 'img/logo.svg',
      colorMode: {
        respectPrefersColorScheme: true,
      },
      navbar: {
        title: 'hyrule',
        logo: {
          alt: 'hyrule logo',
          src: 'img/logo.svg',
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
