const isProd = process.env.NODE_ENV === "production";

/** @type {import('next').NextConfig} */
const nextConfig = {
  // Static export in production: the Go binary embeds web/out and serves it (SPEC §5).
  // In dev we run a normal Next server and proxy /api and /ws to Go on :8080.
  ...(isProd ? { output: "export" } : {}),
  ...(isProd
    ? {}
    : {
        async rewrites() {
          const api = process.env.API_ORIGIN || "http://localhost:8080";
          return [
            { source: "/api/:path*", destination: `${api}/api/:path*` },
            { source: "/ws", destination: `${api}/ws` },
          ];
        },
      }),
};

export default nextConfig;
