import {
  BookOpen,
  Braces,
  Clock3,
  Compass,
  Plug,
  Users,
} from 'lucide-react';
import type { BaseLayoutProps } from 'fumadocs-ui/layouts/shared';

export function baseOptions(): BaseLayoutProps {
  return {
    nav: {
      title: (
        <span className="isodo-brand">
          <img className="isodo-brand-mark" src="/isodo-mascot.png" alt="" />
          <span>isodo</span>
        </span>
      ),
    },
    githubUrl: 'https://github.com/DROWNING2003/Isodo',
    links: [
      { icon: <BookOpen />, text: '文档', url: '/docs', active: 'nested-url' },
      { icon: <Compass />, text: '指南', url: '/docs/guides/quickstart', active: 'nested-url' },
      { icon: <Braces />, text: 'API 参考', url: '/docs/api-reference', active: 'nested-url' },
      { icon: <Plug />, text: '集成', url: '/docs/integrations/casdoor', active: 'nested-url' },
      { icon: <Clock3 />, text: '更新记录', url: '/docs/changelog', active: 'url' },
      { icon: <Users />, text: '社区', url: '/docs/community', active: 'url' },
    ],
  };
}
