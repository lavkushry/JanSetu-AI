import type { Metadata } from 'next';
import './globals.css';
export const metadata: Metadata = {
  title: 'JanSetu — Your city, together',
  description:
    'Local conversations and accountable service progress. Synthetic local demonstration.',
};
export default function Layout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en-IN" suppressHydrationWarning>
      <body>
        <a className="skip-link" href="#main">
          Skip to content
        </a>
        {children}
      </body>
    </html>
  );
}
