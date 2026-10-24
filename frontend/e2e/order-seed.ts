import { Page } from '@playwright/test';

export interface SeededOrder {
  id: number;
  number: string;
  /** Variation codes of the order's two lines, in line order. */
  codes: [string, string];
  /** The STL bytes uploaded to each variation. */
  stl: [string, string];
}

/**
 * Makes two designs with one variation each (an STL uploaded to both) and a placed order of the two, through
 * the API as the signed-in page's user. The page must be signed in and on an app address.
 */
export async function seedOrder(page: Page, label: string): Promise<SeededOrder> {
  return page.evaluate(async (label) => {
    const s = JSON.parse(sessionStorage.getItem('cadportal.session') ?? '{}');
    const auth = { Authorization: `Bearer ${s.token}` };
    const call = async (method: string, path: string, body?: unknown) => {
      const r = await fetch(`/api/v1${path}`, {
        method,
        headers: { ...auth, ...(body === undefined ? {} : { 'Content-Type': 'application/json' }) },
        body: body === undefined ? undefined : JSON.stringify(body),
      });
      const t = await r.text();
      if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${t}`);
      return t ? JSON.parse(t) : null;
    };
    const tag = `${Date.now()}`.slice(-7);
    const rings = (await call('GET', '/masters/categories')).find((c: { name: string }) => c.name === 'Rings');
    const stl: [string, string] = [`solid ${label}-a-${tag}\nendsolid\n`.repeat(30), `solid ${label}-b-${tag}\nendsolid\n`.repeat(50)];
    const codes: string[] = [];
    const vids: number[] = [];
    for (const [i, content] of stl.entries()) {
      const d = await call('POST', '/designs', { name: `${label} ${tag} ${i}`, category_id: rings.id, tag_ids: [] });
      const v = await call('POST', `/designs/${d.id}/variations`, { volume_mm3: '200' });
      const fd = new FormData();
      fd.append('file', new Blob([content]), `part-${i}.stl`);
      const up = await fetch(`/api/v1/variations/${v.id}/upload-cad`, { method: 'POST', headers: auth, body: fd });
      if (up.status !== 201) throw new Error(`upload: ${up.status}`);
      codes.push(v.code);
      vids.push(v.id);
    }
    const order = await call('POST', '/orders', {
      name: `${label} ${tag}`,
      metal_code: '18KR',
      stone_code: '2HD',
      place: true,
      items: vids.map((variation_id, i) => ({ variation_id, quantity: i + 1 })),
    });
    return { id: order.id, number: order.number, codes: codes as [string, string], stl };
  }, label);
}
