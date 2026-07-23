import "./globals.css";

export const metadata = {
  title: "Yaver Serverless Todo",
  description: "Todo app backed by Yaver Serverless Lite",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
