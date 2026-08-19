// ─── Landing page content ────────────────────────────────────────────────────
// Ported from the reference site's data module and rewritten for Vigil.
// Structure is deliberately identical so the components stay 1:1 with the
// reference; only the strings, colours, and logos are ours.

// Marquee logos — the MCP clients Vigil actually governs, rather than the
// reference's advertising clients. These are the surfaces an agent reaches us
// through, which is the honest equivalent of a "worked with" wall.
export const brands = [
    { name: "claude", label: "Claude" },
    { name: "cursor", label: "Cursor" },
    { name: "vscode", label: "VS Code" },
    { name: "claude-code", label: "Claude Code" },
    { name: "mcp", label: "MCP" },
    { name: "featherless", label: "Featherless" },
    { name: "opentelemetry", label: "OTel" },
    { name: "clickhouse", label: "ClickHouse" },
];

// Marquee tile backgrounds — Vigil's palette rather than the reference's.
export const colors = [
    "var(--color-orange)",
    "var(--color-ink)",
    "var(--color-ember)",
    "var(--color-slate)",
    "var(--color-rust)",
    "var(--color-orange-dark)",
];

export const SOCIAL_ICONS = [
    {
        href: 'https://github.com/Aaditya1273/Vigil',
        label: 'GitHub',
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 24 24" fill="none" data-wiggle-target="" aria-hidden="true"><path d="M12 .5C5.73.5.5 5.73.5 12c0 5.08 3.29 9.39 7.86 10.91.58.11.79-.25.79-.56 0-.28-.01-1.02-.02-2-3.2.69-3.88-1.54-3.88-1.54-.52-1.33-1.28-1.69-1.28-1.69-1.05-.71.08-.7.08-.7 1.16.08 1.77 1.19 1.77 1.19 1.03 1.76 2.7 1.25 3.36.96.1-.75.4-1.25.73-1.54-2.55-.29-5.23-1.28-5.23-5.68 0-1.25.45-2.28 1.19-3.08-.12-.29-.52-1.46.11-3.05 0 0 .97-.31 3.18 1.18a11 11 0 0 1 5.79 0c2.2-1.49 3.17-1.18 3.17-1.18.63 1.59.23 2.76.12 3.05.74.8 1.18 1.83 1.18 3.08 0 4.41-2.68 5.38-5.24 5.67.41.35.78 1.05.78 2.12 0 1.53-.02 2.77-.02 3.15 0 .31.21.68.8.56A11.51 11.51 0 0 0 23.5 12C23.5 5.73 18.27.5 12 .5Z" fill="currentColor"/></svg>'
    },
    {
        href: '#docs',
        label: 'Docs',
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 24 24" fill="none" data-wiggle-target="" aria-hidden="true"><path d="M4 3.5A1.5 1.5 0 0 1 5.5 2H15l5 5v14.5a1.5 1.5 0 0 1-1.5 1.5h-13A1.5 1.5 0 0 1 4 21.5v-18Zm10.5.9V8h3.6L14.5 4.4ZM7.5 11h9v1.6h-9V11Zm0 4h9v1.6h-9V15Zm0-8h4v1.6h-4V7Z" fill="currentColor"/></svg>'
    },
    {
        href: '#security',
        label: 'Security',
        svg: '<svg xmlns="http://www.w3.org/2000/svg" width="30" height="30" viewBox="0 0 24 24" fill="none" data-wiggle-target="" aria-hidden="true"><path d="M12 1.5 3.5 5v6.3c0 5.2 3.6 10 8.5 11.2 4.9-1.2 8.5-6 8.5-11.2V5L12 1.5Zm0 2.2 6.5 2.7v4.9c0 4.1-2.7 8-6.5 9.1-3.8-1.1-6.5-5-6.5-9.1V6.4L12 3.7Zm-1 5.1v4h2v-4h-2Zm0 6v2h2v-2h-2Z" fill="currentColor"/></svg>'
    }
];

// The five checks that run on every governed tool call. Replaces the
// reference's agency service cards.
export const CARDS_DATA = [
    {
        color: 'green',
        sticker: 'camera',
        title: 'intent',
        services: ['Declared session intent', 'Allowed / denied tools', 'Resource categories', 'Network policy', 'Secret-access policy', 'Explainable verdicts', 'Deny always wins']
    },
    {
        color: 'darkblue',
        sticker: 'phone',
        title: 'cost',
        services: ['Rolling burn rate', 'Projected total', 'Time to breach', 'Soft limit reroute', 'Hard limit block', 'Per-session isolation']
    },
    {
        color: 'orange',
        sticker: 'smiley',
        title: 'behaviour',
        services: ['Infinite tool loop', 'Retry storm', 'Latency spike', 'Tool timeout', 'Agent stuck', 'Budget exceeded']
    },
    {
        color: 'maroon',
        sticker: 'hand',
        title: 'judgement',
        services: ['Only when uncertain', 'Strict schema validation', 'Range + enum checks', 'Retry once, then fall back', 'May tighten, never relax']
    },
    {
        color: 'pink',
        sticker: 'heart',
        title: 'audit',
        services: ['SHA-256 hash chain', 'Every decision, allows too', 'Tamper detection', 'Reorder detection', 'Offline verification']
    }
];

// ─── Wiggle Intensity Config ────────────────────────────────────────────────
export const WIGGLE_CONFIG = {
    logoTruus: 4,
    socials: 5,
    jobHeading: 1,
    googleMap: 1,
    email: 1,
    whatsapp: 1,
};

// ─── Animation Configurations ─────────────────────────────────────────────
export const ANIMATION_CONFIG = {
    transitionScribble: {
        strokeWidthStart: "8%",
        strokeWidthMax: "31%",
        scale: 0.7,
        durationIn: 2.2,
        durationOut: 2.7
    }
};
