// Small deterministic layout helpers for the network map. No dependencies: the same input
// always gives the same picture, so the map doesn't jump around between refreshes.

export interface LNode {
  id: string;
  w: number;
  h: number;
  /** Nodes of one group start next to each other, which keeps clusters together. */
  group?: string;
}
export interface LEdge {
  source: string;
  target: string;
}
export interface Pos {
  x: number;
  y: number;
}

/** Top-left positions on a circle (or ellipse), in the given order. */
export function circleLayout(nodes: LNode[], radius?: number): Map<string, Pos> {
  const out = new Map<string, Pos>();
  const n = nodes.length;
  if (!n) return out;
  const widest = Math.max(...nodes.map((x) => Math.max(x.w, x.h)));
  const r = radius ?? Math.max(180, ((widest + 28) * n) / (2 * Math.PI));
  nodes.forEach((node, i) => {
    const a = (i / n) * Math.PI * 2 - Math.PI / 2;
    out.set(node.id, { x: Math.cos(a) * r * 1.25 - node.w / 2, y: Math.sin(a) * r - node.h / 2 });
  });
  return out;
}

/**
 * Force-directed layout (Fruchterman-Reingold) followed by overlap removal.
 * Connected devices end up close, unconnected ones apart, and boxes never overlap.
 */
export function forceLayout(nodes: LNode[], edges: LEdge[]): Map<string, Pos> {
  const n = nodes.length;
  const out = new Map<string, Pos>();
  if (!n) return out;
  if (n === 1) {
    out.set(nodes[0].id, { x: -nodes[0].w / 2, y: -nodes[0].h / 2 });
    return out;
  }
  const sorted = [...nodes].sort((a, b) => (a.group ?? "").localeCompare(b.group ?? "") || a.id.localeCompare(b.id));
  const idx = new Map(sorted.map((x, i) => [x.id, i]));
  const cell = 230;
  const area = n * cell * cell * 0.55;
  const k = Math.sqrt(area / n);
  const px = new Float64Array(n);
  const py = new Float64Array(n);
  const start = Math.max(160, (n * cell) / (2 * Math.PI) / 1.6);
  sorted.forEach((_, i) => {
    const a = (i / n) * Math.PI * 2;
    // A little deterministic wobble so symmetric inputs don't sit exactly on top of each other.
    const wob = 1 + 0.12 * Math.sin(i * 12.9898);
    px[i] = Math.cos(a) * start * wob;
    py[i] = Math.sin(a) * start * wob * 0.75;
  });
  const links: [number, number][] = [];
  for (const e of edges) {
    const a = idx.get(e.source);
    const b = idx.get(e.target);
    if (a !== undefined && b !== undefined && a !== b) links.push([a, b]);
  }
  const dx = new Float64Array(n);
  const dy = new Float64Array(n);
  const iterations = n > 150 ? 120 : 260;
  let temp = k * 2;
  for (let it = 0; it < iterations; it++) {
    dx.fill(0);
    dy.fill(0);
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        let ex = px[i] - px[j];
        let ey = py[i] - py[j];
        let d = Math.hypot(ex, ey);
        if (d < 0.01) {
          ex = Math.sin(i + j + 1);
          ey = Math.cos(i * 3 + j);
          d = 1;
        }
        const f = (k * k) / d;
        dx[i] += (ex / d) * f;
        dy[i] += (ey / d) * f;
        dx[j] -= (ex / d) * f;
        dy[j] -= (ey / d) * f;
      }
    }
    for (const [a, b] of links) {
      const ex = px[a] - px[b];
      const ey = py[a] - py[b];
      const d = Math.max(Math.hypot(ex, ey), 0.01);
      const f = (d * d) / k;
      dx[a] -= (ex / d) * f;
      dy[a] -= (ey / d) * f;
      dx[b] += (ex / d) * f;
      dy[b] += (ey / d) * f;
    }
    for (let i = 0; i < n; i++) {
      dx[i] -= px[i] * 0.04; // gravity keeps unconnected devices from drifting away
      dy[i] -= py[i] * 0.04;
      const d = Math.hypot(dx[i], dy[i]);
      if (d > 0) {
        const m = Math.min(d, temp) / d;
        px[i] += dx[i] * m;
        py[i] += dy[i] * m;
      }
    }
    temp *= 0.985;
  }
  // Remove overlaps: push boxes apart along the axis with the smaller overlap.
  const pad = 26;
  for (let pass = 0; pass < 60; pass++) {
    let moved = false;
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        const wi = sorted[i].w, hi = sorted[i].h, wj = sorted[j].w, hj = sorted[j].h;
        const ox = (wi + wj) / 2 + pad - Math.abs(px[i] - px[j]);
        const oy = (hi + hj) / 2 + pad - Math.abs(py[i] - py[j]);
        if (ox > 0 && oy > 0) {
          moved = true;
          if (ox < oy) {
            const s = (px[i] >= px[j] ? 1 : -1) * (ox / 2 + 0.5);
            px[i] += s;
            px[j] -= s;
          } else {
            const s = (py[i] >= py[j] ? 1 : -1) * (oy / 2 + 0.5);
            py[i] += s;
            py[j] -= s;
          }
        }
      }
    }
    if (!moved) break;
  }
  sorted.forEach((node, i) => out.set(node.id, { x: px[i] - node.w / 2, y: py[i] - node.h / 2 }));
  return out;
}

export interface Box {
  id: string;
  w: number;
  h: number;
}

/**
 * Places boxes around a central hub: a row below it for a few boxes, otherwise a ring.
 * Returns top-left positions for the boxes and the hub.
 */
export function hubLayout(boxes: Box[], hub: { w: number; h: number }): { hub: Pos; boxes: Map<string, Pos> } {
  const out = new Map<string, Pos>();
  const hubPos = { x: -hub.w / 2, y: -hub.h / 2 };
  const gap = 56;
  if (!boxes.length) return { hub: hubPos, boxes: out };
  if (boxes.length <= 3) {
    const total = boxes.reduce((s, b) => s + b.w, 0) + gap * (boxes.length - 1);
    let x = -total / 2;
    const top = hub.h / 2 + 90;
    for (const b of boxes) {
      out.set(b.id, { x, y: top });
      x += b.w + gap;
    }
    return { hub: hubPos, boxes: out };
  }
  // Ring: biggest boxes are spread evenly so neighbours of different sizes don't collide.
  const sorted = [...boxes].sort((a, b) => b.w * b.h - a.w * a.h);
  const order: Box[] = [];
  // Interleave large and small boxes around the ring.
  let lo = 0;
  let hi = sorted.length - 1;
  while (lo <= hi) {
    order.push(sorted[lo++]);
    if (lo <= hi) order.push(sorted[hi--]);
  }
  const arc = order.map((b) => Math.hypot(b.w, b.h) + gap);
  const perimeter = arc.reduce((s, v) => s + v, 0);
  const biggest = Math.max(...order.map((b) => Math.hypot(b.w, b.h)));
  const r = Math.max(perimeter / (2 * Math.PI), Math.hypot(hub.w, hub.h) / 2 + biggest / 2 + gap);
  let acc = 0;
  order.forEach((b, i) => {
    const a = ((acc + arc[i] / 2) / perimeter) * Math.PI * 2 - Math.PI / 2;
    acc += arc[i];
    out.set(b.id, { x: Math.cos(a) * r - b.w / 2, y: Math.sin(a) * r * 0.82 - b.h / 2 });
  });
  return { hub: hubPos, boxes: out };
}

/** Grid of equally sized cells inside a box; returns the cell positions and the box size. */
export function gridInBox(count: number, cell: { w: number; h: number }, opts: { maxCols?: number; gap?: number; pad?: number; header?: number } = {}) {
  const { maxCols = 3, gap = 12, pad = 16, header = 34 } = opts;
  const cols = Math.max(1, Math.min(maxCols, Math.ceil(Math.sqrt(count))));
  const rows = Math.ceil(count / cols);
  const positions: Pos[] = [];
  for (let i = 0; i < count; i++) {
    positions.push({ x: pad + (i % cols) * (cell.w + gap), y: header + pad / 2 + Math.floor(i / cols) * (cell.h + gap) });
  }
  return {
    positions,
    w: pad * 2 + cols * cell.w + (cols - 1) * gap,
    h: header + pad + rows * cell.h + (rows - 1) * gap,
  };
}
