import React from 'react'

export type IconName =
  | 'dashboard'
  | 'dns'
  | 'server'
  | 'router'
  | 'alt_route'
  | 'layers'
  | 'history'
  | 'policy'
  | 'shield'
  | 'speed'
  | 'settings'
  | 'menu_book'
  | 'add_circle'
  | 'search'
  | 'bolt'
  | 'flash_on'
  | 'notifications'
  | 'sync'
  | 'sync_alt'
  | 'download'
  | 'timer'
  | 'pie_chart'
  | 'hub'
  | 'check_circle'
  | 'warning'
  | 'error'
  | 'info'
  | 'power_settings_new'
  | 'verified'
  | 'groups'
  | 'arrow_forward'
  | 'refresh'
  | 'terminal'
  | 'close'
  | 'cancel'
  | 'key'
  | 'copy'
  | 'content_copy'
  | 'play_arrow'
  | 'delete'
  | 'visibility'
  | 'troubleshoot'
  | 'block'
  | 'rocket_launch'

interface IconProps {
  name: IconName | string
  className?: string
  size?: number | string
}

export default function Icon({ name, className = '', size = 18 }: IconProps) {
  const s = typeof size === 'number' ? `${size}px` : size

  const renderPath = () => {
    switch (name) {
      case 'dashboard':
        return (
          <path
            d="M4 4h7v7H4V4zm0 9h7v7H4v-7zm9-9h7v7h-7V4zm0 9h7v7h-7v-7z"
            fill="currentColor"
          />
        )
      case 'dns':
      case 'server':
        return (
          <>
            <rect x="3" y="4" width="18" height="6" rx="2" stroke="currentColor" strokeWidth="2" fill="none" />
            <rect x="3" y="14" width="18" height="6" rx="2" stroke="currentColor" strokeWidth="2" fill="none" />
            <circle cx="7" cy="7" r="1" fill="currentColor" />
            <circle cx="7" cy="17" r="1" fill="currentColor" />
            <line x1="14" y1="7" x2="17" y2="7" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            <line x1="14" y1="17" x2="17" y2="17" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'router':
        return (
          <>
            <rect x="2" y="11" width="20" height="9" rx="2.5" stroke="currentColor" strokeWidth="2" fill="none" />
            <circle cx="6" cy="15.5" r="1" fill="currentColor" />
            <circle cx="10" cy="15.5" r="1" fill="currentColor" />
            <circle cx="14" cy="15.5" r="1" fill="currentColor" />
            <path d="M6 11V4M18 11V4" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'alt_route':
        return (
          <>
            <circle cx="6" cy="18" r="3" stroke="currentColor" strokeWidth="2" fill="none" />
            <circle cx="18" cy="6" r="3" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M9 18h3a3 3 0 0 0 3-3V9a3 3 0 0 1 3-3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            <circle cx="18" cy="18" r="3" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M12 18h3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'layers':
        return (
          <>
            <path d="M12 2L2 7l10 5 10-5-10-5z" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
            <path d="M2 17l10 5 10-5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
            <path d="M2 12l10 5 10-5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
          </>
        )
      case 'history':
        return (
          <>
            <path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
            <path d="M3 3v5h5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            <path d="M12 7v5l3 3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </>
        )
      case 'policy':
      case 'shield':
        return (
          <path
            d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            fill="none"
          />
        )
      case 'speed':
        return (
          <>
            <path d="M22 12A10 10 0 0 0 2 12" stroke="currentColor" strokeWidth="2" strokeLinecap="round" fill="none" />
            <path d="M12 12l4-4" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
            <circle cx="12" cy="12" r="2" fill="currentColor" />
          </>
        )
      case 'settings':
        return (
          <path
            d="M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6zm7.4 0c.2-.6.4-1.3.4-2s-.2-1.4-.4-2l2.1-1.6c.2-.2.3-.4.2-.6l-2-3.5c-.1-.2-.4-.3-.6-.2l-2.4 1c-.5-.4-1.1-.7-1.7-.9l-.4-2.6c0-.2-.2-.4-.5-.4h-4c-.3 0-.5.2-.5.4l-.4 2.6c-.6.2-1.2.5-1.7.9l-2.4-1c-.2-.1-.5 0-.6.2l-2 3.5c-.1.2 0 .4.2.6l2.1 1.6c-.2.6-.4 1.3-.4 2s.2 1.4.4 2l-2.1 1.6c-.2.2-.3.4-.2.6l2 3.5c.1.2.4.3.6.2l2.4-1c.5.4 1.1.7 1.7.9l.4 2.6c0 .2.2.4.5.4h4c.3 0 .5-.2.5-.4l.4-2.6c.6-.2 1.2-.5 1.7-.9l2.4 1c.2.1.5 0 .6-.2l2-3.5c.1-.2 0-.4-.2-.6l-2.1-1.6z"
            stroke="currentColor"
            strokeWidth="1.5"
            strokeLinejoin="round"
            fill="none"
          />
        )
      case 'menu_book':
        return (
          <path
            d="M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1-2.5-2.5zm0 0H20M9 6h7M9 10h7"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            fill="none"
          />
        )
      case 'add_circle':
        return (
          <>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M12 8v8M8 12h8" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'search':
        return (
          <>
            <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M21 21l-4.35-4.35" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </>
        )
      case 'bolt':
      case 'flash_on':
        return (
          <path
            d="M13 2L3 14h9l-1 8 10-12h-9l1-8z"
            stroke="currentColor"
            strokeWidth="2"
            strokeLinecap="round"
            strokeLinejoin="round"
            fill="currentColor"
          />
        )
      case 'notifications':
        return (
          <>
            <path
              d="M18 8A6 6 0 0 0 6 8c0 7-3 9-3 9h18s-3-2-3-9"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              fill="none"
            />
            <path d="M13.73 21a2 2 0 0 1-3.46 0" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'sync':
      case 'refresh':
        return (
          <>
            <path d="M21.5 2v6h-6M2.5 22v-6h6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            <path
              d="M20 15a8 8 0 0 1-13.8 2.8L2.5 16M4 9a8 8 0 0 1 13.8-2.8L21.5 8"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
              fill="none"
            />
          </>
        )
      case 'sync_alt':
        return (
          <>
            <path d="M18 8L21 5l-3-3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            <path d="M3 5h18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            <path d="M6 16l-3 3 3 3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
            <path d="M21 19H3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'download':
        return (
          <>
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
          </>
        )
      case 'timer':
        return (
          <>
            <circle cx="12" cy="13" r="8" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M12 9v4l2.5 2.5M10 2h4M12 2v3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'pie_chart':
        return (
          <>
            <path d="M21.21 15.89A10 10 0 1 1 8 2.83" stroke="currentColor" strokeWidth="2" strokeLinecap="round" fill="none" />
            <path d="M22 12A10 10 0 0 0 12 2v10z" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="currentColor" fillOpacity="0.2" />
          </>
        )
      case 'hub':
        return (
          <>
            <circle cx="12" cy="12" r="3" stroke="currentColor" strokeWidth="2" fill="none" />
            <circle cx="4" cy="6" r="2" fill="currentColor" />
            <circle cx="20" cy="6" r="2" fill="currentColor" />
            <circle cx="12" cy="20" r="2" fill="currentColor" />
            <path d="M6 7l4 3.5M18 7l-4 3.5M12 15v3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
          </>
        )
      case 'check_circle':
      case 'verified':
        return (
          <>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M8.5 12.5l2.5 2.5 5-5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
          </>
        )
      case 'warning':
        return (
          <>
            <path d="M12 2L1 21h22L12 2z" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
            <path d="M12 9v5M12 18h.01" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </>
        )
      case 'error':
        return (
          <>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M12 8v5M12 16h.01" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </>
        )
      case 'info':
        return (
          <>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M12 16v-4M12 8h.01" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </>
        )
      case 'power_settings_new':
        return (
          <>
            <path d="M18.36 6.64a9 9 0 1 1-12.73 0" stroke="currentColor" strokeWidth="2" strokeLinecap="round" fill="none" />
            <path d="M12 2v9" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
          </>
        )
      case 'groups':
        return (
          <>
            <circle cx="9" cy="7" r="4" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M3 21v-2a4 4 0 0 1 4-4h4a4 4 0 0 1 4 4v2" stroke="currentColor" strokeWidth="2" strokeLinecap="round" fill="none" />
            <circle cx="17" cy="11" r="3" stroke="currentColor" strokeWidth="1.5" fill="none" />
            <path d="M17 15h2a3 3 0 0 1 3 3v2" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" fill="none" />
          </>
        )
      case 'close':
      case 'cancel':
        return (
          <>
            <line x1="18" y1="6" x2="6" y2="18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            <line x1="6" y1="6" x2="18" y2="18" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'copy':
      case 'content_copy':
        return (
          <>
            <rect x="9" y="9" width="13" height="13" rx="2" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'troubleshoot':
        return (
          <>
            <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="2" fill="none" />
            <path d="M21 21l-4.35-4.35" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" />
            <path d="M8 11h6M11 8v6" stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" />
          </>
        )
      case 'block':
        return (
          <>
            <circle cx="12" cy="12" r="9" stroke="currentColor" strokeWidth="2" fill="none" />
            <line x1="5.64" y1="5.64" x2="18.36" y2="18.36" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'rocket_launch':
        return (
          <path
            d="M4.5 16.5c-1.5 1.26-2 5-2 5s3.74-.5 5-2c.71-.84.7-2.13-.09-2.91a2.18 2.18 0 0 0-2.91-.09zM12 15l-3-3m0 0l2-2m-2 2l-2-2m15-4c0 0-2-6-10-6S4 6 4 6s0 8 6 10c0 0 2 0 4-2l4-4z"
            stroke="currentColor"
            strokeWidth="1.75"
            strokeLinecap="round"
            strokeLinejoin="round"
            fill="none"
          />
        )
      case 'play_arrow':
        return (
          <polygon points="5,3 19,12 5,21" fill="currentColor" />
        )
      case 'delete':
        return (
          <>
            <path d="M3 6h18M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" fill="none" />
            <line x1="10" y1="11" x2="10" y2="17" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
            <line x1="14" y1="11" x2="14" y2="17" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      case 'visibility':
        return (
          <>
            <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" stroke="currentColor" strokeWidth="2" fill="none" strokeLinecap="round" strokeLinejoin="round" />
            <circle cx="12" cy="12" r="3" stroke="currentColor" strokeWidth="2" fill="none" />
          </>
        )
      case 'visibility_off':
        return (
          <>
            <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" stroke="currentColor" strokeWidth="2" fill="none" strokeLinecap="round" strokeLinejoin="round" />
            <line x1="1" y1="1" x2="23" y2="23" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
          </>
        )
      default:
        return (
          <circle cx="12" cy="12" r="6" stroke="currentColor" strokeWidth="2" fill="none" />
        )
    }
  }

  return (
    <svg
      width={s}
      height={s}
      viewBox="0 0 24 24"
      fill="none"
      xmlns="http://www.w3.org/2000/svg"
      className={`inline-block flex-shrink-0 align-middle ${className}`}
      aria-hidden="true"
    >
      {renderPath()}
    </svg>
  )
}
