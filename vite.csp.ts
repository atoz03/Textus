import type { Plugin } from "vite";
import windowCsp from "./src/platform/window-csp.json";

type Directives = Record<string, string[]>;

/** A policy as the header and the `<meta>` write it. */
function cspString(directives: Directives): string {
  return Object.entries(directives)
    .map(([name, sources]) => [name, ...sources].join(" "))
    .join("; ");
}

/**
 * The window's policy for the Vite dev server. `mygo dev` loads the dev
 * server's page as it is, and the e2e flows run under it too: the server
 * sends it, with its own HMR socket.
 */
export function devServerCsp(): string {
  return cspString({
    ...windowCsp,
    "connect-src": [...windowCsp["connect-src"], "ws://localhost:*"],
  });
}

/**
 * The window's policy in the built app's pages. MyGo serves them with no
 * policy of its own, so each carries it in a `<meta>`, ahead of anything
 * it guards.
 */
export function windowCspMeta(): Plugin {
  return {
    name: "textus-window-csp",
    apply: "build",
    transformIndexHtml: {
      order: "pre",
      handler: () => [
        {
          tag: "meta",
          attrs: {
            "http-equiv": "Content-Security-Policy",
            content: cspString(windowCsp),
          },
          injectTo: "head-prepend",
        },
      ],
    },
  };
}
