/**
 * A backstop for anything that still calls the backend directly in the
 * browser: a rejection it can catch, never a request. The platform modules
 * already use their browser stand-ins here.
 */
export function installNativeStandIn(): void {
  if (window.__TEXTUS_HOST__) return;
  window.__TEXTUS_HOST__ = {
    invoke: (command: string) =>
      Promise.reject(new Error(`${command}: not available in the browser`)),
  };
}
