// Mounts and focuses a node. Used via `use:focus` to avoid Svelte's
// a11y_autofocus warning while keeping autofocus behaviour.
export function focus(node) {
  node.focus();
}
