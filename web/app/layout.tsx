import type { Metadata } from "next";
import { Outfit } from "next/font/google";
import React from "react";
import "./globals.css";

const outfit = Outfit({
  subsets: ["latin"],
  weight: ["300", "400", "500", "600", "700"],
  display: "swap",
});

export const metadata: Metadata = {
  title: "markt — Used electronics copilot",
  description: "Buy used electronics without overpaying. markt scans listings, estimates fair value, flags risks, and guides seller outreach.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={outfit.className}>
      <body>{children}</body>
    </html>
  );
}
