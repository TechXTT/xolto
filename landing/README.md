# xolto-landing

`xolto-landing` is the standalone Next.js marketing site for xolto.

## Requirements

- Node.js 18+
- npm

## Local setup

```bash
npm install
cp .env.example .env.local
```

Set `NEXT_PUBLIC_APP_URL` in `.env.local` to the app origin you want the CTA links to target.

## Development

```bash
npm run dev
```

The landing site runs on `http://localhost:3001` by default.

## Production build

```bash
npm run build
npm run start
```
