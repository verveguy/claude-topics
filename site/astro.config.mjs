import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import starlightLinksValidator from 'starlight-links-validator';

// Published by .github/workflows/docs.yml to GitHub Pages. verveguy/claude-topics is a
// project site under the verveguy account, whose user site (verveguy.github.io) has the
// custom domain v3rv.com, so this one is served at https://v3rv.com/claude-topics/ with no
// DNS of its own. Internal links therefore carry the base: /claude-topics/<page>.
export default defineConfig({
  site: 'https://v3rv.com',
  base: '/claude-topics',
  trailingSlash: 'never',
  build: { format: 'file' },
  integrations: [
    starlight({
      title: 'claude-topics',
      description:
        'Pick up, put down, and hand off long-running Claude Code work — from anywhere, including your phone.',
      favicon: '/favicon.svg',
      social: [
        { icon: 'github', label: 'GitHub', href: 'https://github.com/verveguy/claude-topics' },
      ],
      editLink: {
        baseUrl: 'https://github.com/verveguy/claude-topics/tree/main/site/',
      },
      customCss: ['./src/styles/custom.css'],
      plugins: [starlightLinksValidator({ errorOnRelativeLinks: false })],
      sidebar: [
        {
          label: 'Start here',
          items: [
            { label: 'Overview', link: '/' },
            { slug: 'start/install' },
            { slug: 'start/first-topic' },
          ],
        },
        {
          label: 'Working with topics',
          items: [
            { slug: 'topics/basics' },
            { slug: 'topics/handoff' },
            { slug: 'topics/fork' },
            { slug: 'topics/claude-args' },
            { slug: 'topics/rename' },
            { slug: 'topics/profiles' },
            { slug: 'topics/adopt' },
            { slug: 'topics/ui' },
          ],
        },
        {
          label: 'Keeping it running',
          items: [
            { slug: 'running/dispatcher' },
            { slug: 'running/daemons' },
            { slug: 'running/cycle' },
            { slug: 'running/restore' },
            { slug: 'running/signing' },
            { slug: 'running/windows' },
          ],
        },
        {
          label: 'Slack',
          items: [
            { slug: 'slack/setup' },
            { slug: 'slack/in-slack' },
            { slug: 'slack/modes' },
            { slug: 'slack/trust' },
            { slug: 'slack/how-it-works' },
          ],
        },
        {
          label: 'Reference',
          items: [
            { slug: 'reference/commands' },
            { slug: 'reference/files' },
            { slug: 'reference/skills' },
            { slug: 'reference/mechanics' },
            { slug: 'reference/development' },
          ],
        },
      ],
    }),
  ],
});
