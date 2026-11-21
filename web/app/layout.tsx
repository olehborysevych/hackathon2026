import type { Metadata } from "next";
import { Press_Start_2P, VT323 } from "next/font/google";
import "./globals.css";

// Fonts are self-hosted at build time (no request to Google from players' browsers).
const px = Press_Start_2P({ weight: "400", subsets: ["latin"], variable: "--font-px", display: "swap" });
const body = VT323({ weight: "400", subsets: ["latin"], variable: "--font-body", display: "swap" });

export const metadata: Metadata = {
  title: "Find the Human",
  description: "Two humans. Three AIs. One table. Find the other human.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en" className={`${px.variable} ${body.variable}`}>
      <body>{children}</body>
    </html>
  );
}
