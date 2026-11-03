import type { Metadata } from 'next';
import { RootProvider } from 'fumadocs-ui/provider/next';
import './globals.css';
import { docsTranslations } from '@/lib/zh-cn';

export const metadata: Metadata = {
  title: {
    default: 'Isodo 文档',
    template: '%s | Isodo 文档',
  },
  description: 'Isodo：为 AI Agent 提供安全、持久、可授权的沙箱工作空间。',
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <RootProvider i18n={{ locale: 'zh-CN', translations: docsTranslations }}>{children}</RootProvider>
      </body>
    </html>
  );
}
