// Action registry: UI components dispatch by string name; implementations
// register themselves at import time. Mirrors the Go dispatch package.
type ActionFn = (payload: unknown) => Promise<void>;

const actions = new Map<string, ActionFn>();

export function registerAction(name: string, fn: ActionFn): void {
  actions.set(name, fn);
}

export async function dispatch(name: string, payload: unknown): Promise<void> {
  const fn = actions.get(name);
  if (!fn) {
    throw new Error(`no action registered for ${name}`);
  }
  await fn(payload);
}
