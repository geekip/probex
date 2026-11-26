import '@interactjs/auto-start';
import '@interactjs/actions/drag';
import interact from '@interactjs/interact';

export function reorderTabs<T extends { id: string | number }>(
  items: T[],
  from: string,
  to: string,
) {
  const ordered = [...items];
  const [item] = ordered.splice(
    items.findIndex((item) => String(item.id) === from),
    1,
  );
  ordered.splice(
    items.findIndex((item) => String(item.id) === to),
    0,
    item,
  );
  return ordered;
}

export function sortableTabs(
  strip: HTMLElement,
  onreorder: (from: string, to: string) => void,
) {
  let grabbed: HTMLElement,
    offset = 0,
    translation = 0;
  const tabs = () => [...strip.querySelectorAll<HTMLElement>('[data-tab-id]')];
  const drag = interact('[data-tab-id]', { context: strip })
    .styleCursor(false)
    .draggable({
      startAxis: 'x',
      lockAxis: 'x',
      ignoreFrom: '.tab-close',
      listeners: {
        start(event) {
          grabbed = event.target;
          offset = event.clientX - grabbed.getBoundingClientRect().left;
          translation = 0;
          grabbed.classList.add('tab-dragging');
          (grabbed.matches('[role="tab"]')
            ? grabbed
            : grabbed.querySelector<HTMLElement>('[role="tab"]')!
          ).click();
        },
        move(event) {
          const rect = grabbed.getBoundingClientRect();
          const left = event.clientX - offset;
          const base = rect.left - translation;
          translation = left - base;
          grabbed.style.transform = `translateX(${translation}px)`;
          const siblings = tabs(),
            index = siblings.indexOf(grabbed);
          const center = left + rect.width / 2;
          const target =
            translation > 0
              ? siblings
                  .slice(index + 1)
                  .reverse()
                  .find((tab) => {
                    const box = tab.getBoundingClientRect();
                    return center > box.left + box.width / 2;
                  })
              : siblings.slice(0, index).find((tab) => {
                  const box = tab.getBoundingClientRect();
                  return center < box.left + box.width / 2;
                });
          if (target) {
            const before = base;
            onreorder(grabbed.dataset.tabId!, target.dataset.tabId!);
            // The keyed item keeps its gesture while Svelte moves the same element.
            queueMicrotask(() => {
              const moved = grabbed.getBoundingClientRect().left - translation;
              translation += before - moved;
              grabbed.style.transform = `translateX(${translation}px)`;
            });
          }
        },
        end() {
          grabbed.style.removeProperty('transform');
          grabbed.classList.remove('tab-dragging');
        },
      },
    });
  function keys(event: KeyboardEvent) {
    if (
      !event.altKey ||
      !event.shiftKey ||
      !['ArrowLeft', 'ArrowRight'].includes(event.key)
    )
      return;
    const tab = (event.target as HTMLElement).closest<HTMLElement>(
      '[data-tab-id]',
    )!;
    const siblings = tabs(),
      index = siblings.indexOf(tab);
    const target = siblings[index + (event.key === 'ArrowLeft' ? -1 : 1)];
    event.preventDefault();
    event.stopPropagation();
    if (target) {
      onreorder(tab.dataset.tabId!, target.dataset.tabId!);
    }
  }
  strip.addEventListener('keydown', keys, true);
  return {
    destroy() {
      strip.removeEventListener('keydown', keys, true);
      drag.unset();
    },
  };
}
