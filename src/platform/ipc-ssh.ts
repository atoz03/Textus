import type * as Generated from "./bindings/plugin-access";
import { callBytes, command } from "./ipc-call";

/** A plugin's links to servers through the user's own ssh (desktop). */
export const ssh = {
  hosts: (id: string) => command("plugin_ssh_hosts", id),
  connect: (id: string, host: string) =>
    command("plugin_ssh_connect", id, host),
  home: (id: string, session: number) =>
    command("plugin_ssh_home", id, session),
  list: (
    id: string,
    session: number,
    path: string,
  ): Promise<Generated.RemoteEntry[]> =>
    command("plugin_ssh_list", id, session, path),
  read: (id: string, session: number, path: string) =>
    callBytes("plugin_ssh_read", { id, session, path }),
  write: (
    id: string,
    session: number,
    path: string,
    contents: string,
    base64: boolean,
  ) => command("plugin_ssh_write", id, session, path, contents, base64),
  create: (id: string, session: number, path: string, folder: boolean) =>
    command("plugin_ssh_create", id, session, path, folder),
  rename: (id: string, session: number, from: string, to: string) =>
    command("plugin_ssh_rename", id, session, from, to),
  remove: (id: string, session: number, path: string) =>
    command("plugin_ssh_remove", id, session, path),
  /** One link, or (null) every link of the plugin. */
  close: (id: string, session: number | null) =>
    command("plugin_ssh_close", id, session),
};
