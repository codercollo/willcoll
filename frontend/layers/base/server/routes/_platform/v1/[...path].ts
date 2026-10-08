// /_platform/v1/** -> platform-api (identity, account, billing, admin).
// Same-origin, so the platform session cookie is first-party to the app.
export default defineEventHandler((event) => {
  const { apiPlatformInternalBase } = useRuntimeConfig(event);
  return proxyToApi(event, apiPlatformInternalBase as string, "/_platform");
});
