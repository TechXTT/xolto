import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "MarktBot — Find the deal before anyone else does",
  description:
    "MarktBot watches European marketplaces, scores fresh listings, and helps you turn a vague buying goal into a sharp, automated search workflow.",
  openGraph: {
    title: "MarktBot — AI-powered marketplace intelligence",
    description:
      "Automated deal hunting across Marktplaats, Vinted, and more. AI scoring, fair-value estimates, and a smart assistant that remembers your brief.",
    type: "website",
  },
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <head>
        <link rel="preconnect" href="https://fonts.googleapis.com" />
        <link rel="preconnect" href="https://fonts.gstatic.com" crossOrigin="anonymous" />
        <link
          href="https://fonts.googleapis.com/css2?family=Instrument+Serif:ital@0;1&family=Inter:wght@400;500;600;700&display=swap"
          rel="stylesheet"
        />
      </head>
      <body>{children}</body>
    </html>
  );
}
