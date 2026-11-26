import { hasTextusAi } from "../../platform/capabilities";
import { addInlineSource } from "../../ai/writing/inline-sources";
import { plugins as pluginsIpc } from "../../platform/ipc";
import {
  optionalStr,
  str,
  type Params,
  type PluginScope,
} from "./broker-scope";

interface Message {
  role: "user" | "assistant";
  content: string;
}

/** A conversation as one prompt: the model is asked for the next turn. */
export function promptOf(value: unknown): string {
  if (typeof value === "string") return value;
  if (!Array.isArray(value) || !value.length)
    throw new Error("messages is a list of { role, content }.");
  const turns = value.map((m: Partial<Message>) => {
    if (
      (m?.role !== "user" && m?.role !== "assistant") ||
      typeof m.content !== "string"
    )
      throw new Error("messages is a list of { role, content }.");
    return m as Message;
  });
  if (turns.length === 1) return turns[0].content;
  return turns
    .map((m) => `${m.role === "user" ? "User" : "Assistant"}: ${m.content}`)
    .concat("Assistant:")
    .join("\n\n");
}

/** A plugin's answer waits this long before the suggestion is dropped. */
const INLINE_WAIT_MS = 5000;

/** Each plugin's inline sources, by `<plugin id>#<its id>`. */
const inline = new Map<string, () => void>();

/** Takes away a stopped plugin's inline sources. */
export function stopPluginAi(plugin: string): void {
  for (const [key, remove] of inline)
    if (key.startsWith(`${plugin}#`)) {
      remove();
      inline.delete(key);
    }
}

/** `textus.ai.complete`, `textus.ai.stream` and `textus.ai.available`:
 *  the user's writing model, for a plugin that is on with `ai.model`. */
export function aiModelHandlers(p: PluginScope) {
  /** `run` with the request's instructions and prompt. */
  const gated = async (
    system: unknown,
    messages: unknown,
    run: (instructions: string, prompt: string) => Promise<string>,
  ): Promise<string> => {
    if (!hasTextusAi) throw new Error("Textus AI isn't available here.");
    return run(optionalStr(system) ?? "", str(promptOf(messages), "messages"));
  };
  return {
    "ai.registerInlineCompletion": ({ id, label }: Params) => {
      const own = str(id, "id");
      const key = `${p.id}#${own}`;
      inline.get(key)?.();
      inline.set(
        key,
        addInlineSource({
          key,
          plugin: p.id,
          label: optionalStr(label) ?? p.name(),
          provide: (request) =>
            Promise.race([
              p
                .request<unknown>("$inlineCompletion", { id: own, ...request })
                .then((text) => (typeof text === "string" ? text : null)),
              new Promise<null>((resolve) =>
                setTimeout(() => resolve(null), INLINE_WAIT_MS),
              ),
            ]),
        }),
      );
    },
    "ai.unregisterInlineCompletion": ({ id }: Params) => {
      const key = `${p.id}#${str(id, "id")}`;
      inline.get(key)?.();
      inline.delete(key);
    },
    "ai.available": async () =>
      hasTextusAi && (await pluginsIpc.aiStatus(p.id)).on,
    "ai.complete": ({ system, messages, cancelId }: Params) =>
      gated(system, messages, (instructions, prompt) =>
        pluginsIpc.aiComplete(
          p.id,
          instructions,
          prompt,
          optionalStr(cancelId) ?? null,
        ),
      ),
    "ai.stream": ({ system, messages, stream, cancelId }: Params) => {
      const id = str(stream, "stream");
      return gated(system, messages, (instructions, prompt) =>
        pluginsIpc.aiStream(
          p.id,
          instructions,
          prompt,
          (text) => {
            void p.request("$aiStreamText", { stream: id, text });
          },
          optionalStr(cancelId) ?? null,
        ),
      );
    },
  };
}
