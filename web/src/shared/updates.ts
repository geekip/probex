export type UpdateScope =
  | { topic: 'nodes'; node_id?: never }
  | { topic: 'commands' | 'forwards'; node_id: string };

export type UpdateNotice = UpdateScope & {
  type: 'subscribed' | 'change' | 'output';
  id?: string;
};

export function updateKey(scope: UpdateScope) {
  return JSON.stringify([scope.topic, scope.node_id]);
}
