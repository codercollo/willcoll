// /v1/** -> rentals-api. Same-origin, so the session cookie is first-party.
export default defineEventHandler((event) => {
  const { apiInternalBase } = useRuntimeConfig(event);
  return proxyToApi(event, apiInternalBase as string);
});
