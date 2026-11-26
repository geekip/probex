import {
  EditorState,
  Facet,
  MapMode,
  RangeSet,
  RangeValue,
  StateEffect,
  StateField,
  Text,
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

// Wrapped layout for these lines can block the browser while measuring the DOM.
const maxWrappedLineLength = 10_000;

export function editorDocument(source: string): {
  doc: Text;
  hasLongLines: boolean;
  extensions: Extension;
} {
  const hasCR = source.includes('\r');
  const lines = source.split(hasCR ? /\r\n?|\n/ : '\n');
  let separator = '\n';
  let exceptions: RangeSet<LineEnding> = RangeSet.empty;
  if (hasCR) {
    const separators = ['\n', '\r\n', '\r'];
    const endings = new Uint8Array(lines.length - 1);
    const counts = new Map<number, number>();
    let offset = 0;
    for (let i = 0; i < endings.length; i++) {
      offset += lines[i].length;
      const ending =
        source[offset] === '\n' ? 0 : source[offset + 1] === '\n' ? 1 : 2;
      endings[i] = ending;
      counts.set(ending, (counts.get(ending) ?? 0) + 1);
      offset += separators[ending].length;
    }
    let dominant = 0;
    let largest = 0;
    for (const [ending, count] of counts) {
      if (count > largest) {
        dominant = ending;
        largest = count;
      }
    }
    separator = separators[dominant];
    if (counts.size > 1) {
      const values = separators.map((value) => new LineEnding(value));
      const ranges: Range<LineEnding>[] = [];
      let position = 0;
      for (let i = 0; i < endings.length; i++) {
        position += lines[i].length;
        const ending = endings[i];
        if (ending !== dominant) ranges.push(values[ending].range(position));
        position++;
      }
      exceptions = RangeSet.of(ranges);
    }
  }
  return {
    doc: Text.of(lines),
    hasLongLines: lines.some((line) => line.length > maxWrappedLineLength),
    extensions: [
      initial.of({ separator, exceptions }),
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
