import type { NextConfig } from "next";

// Backend location. Set VIGIL_BACKEND_URL to point the dashboard at a local
// control plane; the deployed host remains the default so an unconfigured
// production build keeps working.
const BACKEND =
  process.env.VIGIL_BACKEND_URL ||
  process.env.ARGUS_BACKEND_URL ||
  "https://vigil-hydra.onrender.com";

const nextConfig: NextConfig = {
  images: {
    // Unsplash, for the atmospheric plates in the parallax section. Their
    // licence permits commercial use without attribution. Remote rather than
    // vendored so the repo does not carry megabytes of decoration, and
    // allow-listed by host so this cannot become a way to proxy arbitrary
    // images through our optimiser.
    remotePatterns: [
      { protocol: "https", hostname: "images.unsplash.com", pathname: "/**" },
    ],
    formats: ["image/avif", "image/webp"],
  },

  async rewrites() {
    return [
      // OAuth 2.1 AS discovery + endpoints — Claude Web calls these directly on the backend.
      // These rewrites let the frontend dev server proxy them too (useful for local testing).
      { source: "/.well-known/:path*",  destination: `${BACKEND}/.well-known/:path*` },
      { source: "/authorize",           destination: `${BACKEND}/authorize` },
      { source: "/register",            destination: `${BACKEND}/register` },
      { source: "/token",               destination: `${BACKEND}/token` },
      // MCP SSE + Bearer endpoints
      { source: "/api/v1/mcp",          destination: `${BACKEND}/api/v1/mcp` },
      { source: "/api/v1/mcp/bearer",   destination: `${BACKEND}/api/v1/mcp/bearer` },
      // Catch-all for any other /api/v1 not handled by a Next.js route file
      { source: "/api/v1/:path*",       destination: `${BACKEND}/api/v1/:path*` },
    ];
  },
};

export default nextConfig;
