// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  learnSidebar: [
    {
      type: 'category',
      label: 'Learn',
      collapsible: false,
      items: [
        'learn/intro',
        'learn/quick-start',
        'learn/your-first-resource',
        'learn/relations-and-hooks',
        'learn/the-outbox',
        'learn/testing',
        'learn/going-further',
      ],
    },
  ],
  referenceSidebar: [
    {
      type: 'category',
      label: 'Reference',
      collapsible: false,
      items: [
        'reference/store',
        'reference/querying',
        'reference/outbox',
        'reference/resources-yaml',
        'reference/dry-run',
        'reference/testkit',
        'reference/observability',
        'reference/architecture',
        'reference/postgres-rfc',
      ],
    },
  ],
};

export default sidebars;
