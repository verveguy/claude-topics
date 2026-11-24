# claude-topics documentation site

[Astro](https://astro.build) + [Starlight](https://starlight.astro.build), the same setup as the
other v3rv.com project sites (Family Filter, Liminis Models).

**Public:** published to GitHub Pages at https://v3rv.com/claude-topics by
`.github/workflows/docs.yml` on each push to `main` that touches `site/`. It is a project site under
the verveguy account, whose user site carries the v3rv.com domain, so it needs no DNS of its own.

```bash
pnpm install
pnpm dev        # http://localhost:4321/claude-topics
pnpm build      # dist/, and checks every internal link
pnpm preview
```

- This is the documentation. The repo README is a short introduction that links here; keep detail
  here, not in both.
- Pages are markdown in `src/content/docs/<section>/`. Every page needs a `title` in its front
  matter. The sidebar is declared by hand in `astro.config.mjs`; add new pages there.
- Internal links carry the base path: `/claude-topics/topics/handoff`. The build fails on a broken
  one (`starlight-links-validator`).
- Asides with a title put it in brackets: `:::note[Title]`.
- When a command, flag or behaviour changes in `cmd/`, change its page in the same PR, and the
  `topic help` text in `cmd/topic/cli.go`.
