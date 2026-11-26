import {
  EditorState,
  Facet,
  MapMode,
  RangeSet,
  RangeValue,
  StateEffect,
  StateField,
  type Extension,
  type Range,
} from '@codemirror/state';
import { invertedEffects } from '@codemirror/commands';

class LineEnding extends RangeValue {
  startSide = 1;
  endSide = 1;
  point = true;
  mapMode = MapMode.TrackAfter;
  readonly separator: string;
  constructor(separator: string) {
    super();
    this.separator = separator;
  }
  eq(other: LineEnding) {
    return this.separator === other.separator;
  }
}

type Newlines = { separator: string; exceptions: RangeSet<LineEnding> };
const initial = Facet.define<Newlines, Newlines>({
  combine: (values) => values[0],
});
const restore = StateEffect.define<Newlines>();
export const chooseNewline = StateEffect.define<string>();
export const newlineState = StateField.define<Newlines>({
  create: (state) => state.facet(initial),
  update(value, transaction) {
    // Grouped undo effects run newest first. The last snapshot is the
    // state before the entire group, with offsets in the restored document.
    for (let i = transaction.effects.length - 1; i >= 0; i--) {
      const effect = transaction.effects[i];
      if (effect.is(restore)) return effect.value;
    }
    for (const effect of transaction.effects) {
      if (effect.is(chooseNewline))
        return { separator: effect.value, exceptions: RangeSet.empty };
    }
    if (!transaction.docChanged || value.exceptions === RangeSet.empty)
      return value;
    return { ...value, exceptions: value.exceptions.map(transaction.changes) };
  },
});

export function editorDocument(source: string): {
  doc: string;
  extensions: Extension;
} {
  const counts = new Map<string, number>();
  for (const [value] of source.matchAll(/\r\n|\r|\n/g))
    counts.set(value, (counts.get(value) ?? 0) + 1);
  let separator = '\n',
    largest = 0;
  const values = new Map<string, LineEnding>();
  for (const [value, count] of counts) {
    if (count > largest) {
      separator = value;
      largest = count;
    }
    values.set(value, new LineEnding(value));
  }
  let removed = 0;
  const exceptions: Range<LineEnding>[] = [];
  const doc = source.replace(/\r\n|\r|\n/g, (value, offset) => {
    const position = offset - removed;
    if (value !== separator)
      exceptions.push(values.get(value)!.range(position));
    removed += value.length - 1;
    return '\n';
  });
  return {
    doc,
    extensions: [
      initial.of({ separator, exceptions: RangeSet.of(exceptions) }),
      newlineState,
      invertedEffects.of((transaction) =>
        transaction.docChanged ||
        transaction.effects.some((e) => e.is(chooseNewline) || e.is(restore))
          ? [restore.of(transaction.startState.field(newlineState))]
          : [],
      ),
    ],
  };
}
export function sameEditorContent(a: EditorState, b: EditorState): boolean {
  if (a === b) return true;
  if (!a.doc.eq(b.doc)) return false;
  const left = a.field(newlineState);
  const right = b.field(newlineState);
  if (left === right) return true;
  if (left.separator === right.separator)
    return RangeSet.eq([left.exceptions], [right.exceptions]);
  // Every break must be an exception on at least one side when defaults differ.
  if (left.exceptions.size + right.exceptions.size < a.doc.lines - 1)
    return false;
  const cursor = a.doc.iter();
  const leftEndings = left.exceptions.iter();
  const rightEndings = right.exceptions.iter();
  let position = 0;
  while (!cursor.next().done) {
    if (
      cursor.lineBreak &&
      lineEnding(leftEndings, position, left.separator) !==
        lineEnding(rightEndings, position, right.separator)
    )
      return false;
    position += cursor.value.length;
  }
  return true;
}
function lineEnding(
  cursor: ReturnType<RangeSet<LineEnding>['iter']>,
  position: number,
  separator: string,
): string {
  if (!cursor.value || cursor.from !== position) return separator;
  const value = cursor.value.separator;
  cursor.next();
  return value;
}

export function editorContent(state: EditorState): string {
  const { separator, exceptions } = state.field(newlineState);
  const endings = exceptions.iter();
  return state.doc
    .toString()
    .replace(/\n/g, (_value, position) =>
      lineEnding(endings, position, separator),
    );
}
export function newlineLabel(state: EditorState): string {
  const value = state.field(newlineState);
  return value.exceptions !== RangeSet.empty
    ? 'Mixed'
    : value.separator === '\r\n'
      ? 'CRLF'
      : value.separator === '\r'
        ? 'CR'
        : 'LF';
}
