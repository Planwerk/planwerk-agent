import { defineConfig } from 'vitepress'

// VitePress configuration for the planwerk-review documentation site.
//
// The site is organized along the Diátaxis framework
// (https://diataxis.fr/): Tutorials (learning), How-to guides (tasks),
// Reference (lookup), and Explanation (understanding). The `base` matches the
// GitHub Pages project path (https://planwerk.github.io/planwerk-review/);
// without it the asset and link URLs 404 on the published site.
export default defineConfig({
  title: 'planwerk-review',
  description:
    'AI-powered code review and codebase analysis for GitHub repositories.',
  base: '/planwerk-review/',
  cleanUrls: true,
  lastUpdated: true,

  themeConfig: {
    nav: [
      { text: 'Tutorials', link: '/tutorials/' },
      { text: 'How-to', link: '/how-to/' },
      { text: 'Reference', link: '/reference/' },
      { text: 'Explanation', link: '/explanation/' },
    ],

    sidebar: {
      '/tutorials/': [
        {
          text: 'Tutorials',
          items: [
            { text: 'Overview', link: '/tutorials/' },
            { text: 'Getting started', link: '/tutorials/getting-started' },
            {
              text: 'From repo to GitHub issues',
              link: '/tutorials/from-repo-to-issues',
            },
          ],
        },
      ],
      '/how-to/': [
        {
          text: 'How-to guides',
          items: [
            { text: 'Overview', link: '/how-to/' },
            { text: 'Review a pull request', link: '/how-to/review-a-pr' },
            {
              text: 'Analyze a repository',
              link: '/how-to/analyze-a-repository',
            },
            { text: 'Audit a codebase', link: '/how-to/audit-a-codebase' },
            { text: 'Check feature gaps', link: '/how-to/check-feature-gaps' },
            { text: 'Elaborate an issue', link: '/how-to/elaborate-an-issue' },
            { text: 'Generate a prompt', link: '/how-to/generate-a-prompt' },
            { text: 'Implement an issue', link: '/how-to/implement-an-issue' },
            { text: 'Rebase a PR', link: '/how-to/rebase-a-pr' },
            { text: 'Use local mode', link: '/how-to/use-local-mode' },
            {
              text: 'Use the GitHub Action',
              link: '/how-to/use-the-github-action',
            },
            {
              text: 'Install completions & man pages',
              link: '/how-to/install-completions-and-man-pages',
            },
            {
              text: 'Write review patterns',
              link: '/how-to/write-review-patterns',
            },
            {
              text: 'Configure the project',
              link: '/how-to/configure-the-project',
            },
          ],
        },
      ],
      '/reference/': [
        {
          text: 'Reference',
          items: [
            { text: 'Overview', link: '/reference/' },
            { text: 'CLI', link: '/reference/cli' },
            { text: 'Configuration file', link: '/reference/configuration' },
            { text: 'Review patterns', link: '/reference/review-patterns' },
            { text: 'Output format', link: '/reference/output-format' },
            { text: 'GitHub Action', link: '/reference/github-action' },
            {
              text: 'Environment variables & exit codes',
              link: '/reference/environment-variables',
            },
            {
              text: 'Project structure',
              link: '/reference/project-structure',
            },
          ],
        },
      ],
      '/explanation/': [
        {
          text: 'Explanation',
          items: [
            { text: 'Overview', link: '/explanation/' },
            {
              text: 'Concept & architecture',
              link: '/explanation/concept',
            },
            {
              text: 'Review methodology',
              link: '/explanation/review-methodology',
            },
            { text: 'Caching model', link: '/explanation/caching' },
            {
              text: 'Design decisions',
              link: '/explanation/design-decisions',
            },
            { text: 'Roadmap', link: '/explanation/roadmap' },
          ],
        },
      ],
    },

    search: {
      provider: 'local',
    },

    socialLinks: [
      {
        icon: 'github',
        link: 'https://github.com/planwerk/planwerk-review',
      },
    ],

    editLink: {
      pattern:
        'https://github.com/planwerk/planwerk-review/edit/main/docs/:path',
      text: 'Edit this page on GitHub',
    },
  },
})
