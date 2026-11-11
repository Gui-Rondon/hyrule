// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  learnSidebar: [
    {
      type: 'category',
      label: 'Framework',
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
    {
      type: 'category',
      label: 'Platform',
      collapsible: false,
      items: [
        'platform/intro',
        'platform/creating-a-project',
        'platform/creating-services',
        'platform/local-development',
        'platform/ci-and-deploy',
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
        'reference/architecture-decisions',
        'reference/roadmap',
      ],
    },
  ],
};

export default sidebars;
