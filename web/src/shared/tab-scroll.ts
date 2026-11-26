export function scrollTabs(event: WheelEvent) {
  const strip = event.currentTarget as HTMLElement;
  if (strip.scrollWidth <= strip.clientWidth) return;
  event.preventDefault();
  strip.scrollLeft += event.deltaX || event.deltaY;
}
