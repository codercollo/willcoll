// URL-safe slug for a property name: "THE RUNDA'S ARCADE" -> "the-rundas-arcade".
export function slugify(name: string): string {
  return name
    .toLowerCase()
    .replace(/['\u2019]/g, '')
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 60)
}
