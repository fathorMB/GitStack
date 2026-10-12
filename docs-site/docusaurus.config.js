// GitStack documentation site (GIT-223, release rule V6a): English only,
// no translations, no copy inside the instance. Published on GitHub Pages of
// the public mirror from main (see .github/workflows/docs.yml).
//
// The URL and base path default to the project Pages address of the public
// mirror and can be overridden (custom domain, fork) with DOCS_SITE_URL and
// DOCS_BASE_URL at build time.

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'GitStack',
  tagline: 'A personal, on-prem GitHub with a small cloud-hosting stack',
  favicon: 'img/favicon.svg',
  url: process.env.DOCS_SITE_URL || 'https://fathormb.github.io',
  baseUrl: process.env.DOCS_BASE_URL || '/GitStack/',
  organizationName: 'fathorMB',
  projectName: 'GitStack',
  trailingSlash: true,

  // A broken link or anchor fails the build (acceptance criterion of GIT-223).
  onBrokenLinks: 'throw',
  onBrokenAnchors: 'throw',
  onDuplicateRoutes: 'throw',

  markdown: {
    // *.md is plain CommonMark (generated cobra pages and repo docs contain
    // `<...>` placeholders); only *.mdx is parsed as MDX.
    format: 'detect',
    hooks: {
      onBrokenMarkdownLinks: 'throw',
    },
  },

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          routeBasePath: '/',
          sidebarPath: './sidebars.js',
        },
        blog: false,
      }),
    ],
  ],

  themeConfig: {
    navbar: {
      title: 'GitStack',
      items: [
        {to: '/user/', label: 'User', position: 'left'},
        {to: '/admin/', label: 'Admin', position: 'left'},
        {to: '/api/', label: 'API', position: 'left'},
        {to: '/cli/', label: 'CLI', position: 'left'},
        {
          href: 'https://github.com/fathorMB/GitStack',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      copyright: 'GitStack documentation. Server: AGPL-3.0; CLI and skills: Apache-2.0.',
    },
  },
};

module.exports = config;
