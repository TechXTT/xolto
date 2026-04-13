# xolto-app

`xolto-app` is the standalone Next.js product app for xolto.

## Requirements

- Node.js 18+
- npm
- A running xolto API server

## Local setup

```bash
npm install
cp .env.example .env.local
```

Set `NEXT_PUBLIC_API_URL` in `.env.local` to your backend origin.

## Development

```bash
npm run dev
```

The app runs on `http://localhost:3000` by default.

## Production build

```bash
npm run build
npm run start
```

## Routes

- `/missions`
- `/matches`
- `/saved`
- `/settings`

The billing UI expects `NEXT_PUBLIC_STRIPE_PRO_PRICE_ID` and `NEXT_PUBLIC_STRIPE_TEAM_PRICE_ID` until the later POWER rename phase.
