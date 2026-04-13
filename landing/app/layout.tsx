import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "xolto — Used electronics copilot",
  description:
    "Buy used electronics without overpaying. xolto scans second-hand electronics listings, estimates fair value, flags risks, and helps you decide which sellers to contact first.",
  openGraph: {
    title: "xolto — Used electronics copilot",
    description:
      "Mission-based used electronics buying: live matches, fair-value scoring, risk flags, saved comparisons, and seller drafting.",
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
