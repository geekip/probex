import '@interactjs/auto-start';
import '@interactjs/actions/drag';
import interact from '@interactjs/interact';

type DragOptions = { enabled: boolean; move: (dx: number, dy: number) => void };

export function dragWindow(element: HTMLElement, options: DragOptions) {
  const drag = interact(element)
    .styleCursor(false)
    .draggable({
      enabled: options.enabled,
      allowFrom: '.window-drag-area',
      ignoreFrom: '.toolbar-actions,button',
      listeners: {
        move(event) {
          options.move(event.dx, event.dy);
        },
      },
    });
  return {
    update(next: DragOptions) {
      options = next;
      drag.draggable({ enabled: next.enabled });
    },
    destroy() {
      drag.unset();
    },
  };
}
