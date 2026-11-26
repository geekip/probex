export type Axis = 'horizontal' | 'vertical';
export type Layout =
  { id: string } | { id: string; axis: Axis; children: [Layout, Layout] };
export function replace(node: Layout, id: string, value: Layout): Layout {
  if (node.id === id) return value;
  return 'axis' in node
    ? {
        ...node,
        children: [
          replace(node.children[0], id, value),
          replace(node.children[1], id, value),
        ],
      }
    : node;
}
export function remove(node: Layout, id: string): Layout | undefined {
  if (node.id === id) return;
  if (!('axis' in node)) return node;
  const a = remove(node.children[0], id),
    b = remove(node.children[1], id);
  return a && b ? { ...node, children: [a, b] } : a || b;
}
