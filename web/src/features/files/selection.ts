import Selecto from 'selecto';

type SelectionOptions = {
  disabled: boolean;
  selection: string[];
  onchange: (paths: string[]) => void;
};

export function selectRows(element: HTMLElement, options: SelectionOptions) {
  const rows = () => [
    ...element.querySelectorAll<HTMLElement>(
      'tbody tr[data-file-path]:not([data-parent])',
    ),
  ];
  const publish = (selected: (HTMLElement | SVGElement)[]) => {
    if (!options.disabled)
      options.onchange(selected.map((row) => row.dataset.filePath!));
  };
  const selecto = new Selecto({
    dragContainer: element,
    className: 'file-selection-box',
    selectableTargets: [rows],
    selectByClick: false,
    hitRate: 0,
    boundContainer: element,
    checkOverflow: true,
    preventDefault: true,
    preventClickEventOnDrag: true,
    continueSelectWithoutDeselect: true,
    scrollOptions: { container: element, threshold: 24, throttleTime: 16 },
    dragCondition: ({ inputEvent }) => {
      const event = inputEvent as MouseEvent;
      if (
        options.disabled ||
        event.type !== 'mousedown' ||
        event.button !== 0 ||
        (event.target as Element).closest('button, thead')
      )
        return false;
      selecto.setSelectedTargets(
        rows().filter((row) =>
          options.selection.includes(row.dataset.filePath!),
        ),
      );
      selecto.continueSelect = event.ctrlKey || event.metaKey || event.shiftKey;
      return true;
    },
  });
  selecto
    .on('select', ({ selected, rect }) => {
      if (rect.width && rect.height) publish(selected);
    })
    .on('selectEnd', ({ selected, isClick, inputEvent }) => {
      if (
        !isClick ||
        !(inputEvent.target as Element).closest('[data-file-path]')
      )
        publish(selected);
    })
    .on('scroll', ({ direction }) => {
      element.scrollBy(direction[0] * 16, direction[1] * 16);
    });
  return {
    update(value: SelectionOptions) {
      options = value;
    },
    destroy() {
      selecto.destroy();
    },
  };
}
