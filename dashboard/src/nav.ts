import {
  Activity,
  Boxes,
  Gauge,
  Network,
  Route,
  ServerCog,
  Settings2,
  Timer,
  type LucideIcon,
} from 'lucide-react';

export interface NavItem {
  to: string;
  label: string;
  icon: LucideIcon;
  end?: boolean;
}

export const NAV_ITEMS: NavItem[] = [
  { to: '/', label: 'Overview', icon: Gauge, end: true },
  { to: '/sandboxes', label: 'Sandboxes', icon: Boxes },
  { to: '/ipam', label: 'IP Address Management', icon: Network },
  { to: '/gateways', label: 'Gateways', icon: ServerCog },
  { to: '/routes', label: 'Traffic & Routes', icon: Route },
  { to: '/configuration', label: 'Configuration', icon: Settings2 },
  { to: '/observability', label: 'Observability', icon: Activity },
  { to: '/benchmarks', label: 'Benchmarks', icon: Timer },
];
