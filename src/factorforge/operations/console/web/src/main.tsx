import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
  redirect,
} from "@tanstack/react-router";
import { Shell } from "./components/shell";
import { Page, pages } from "./features/page";
import { SessionProvider } from "./features/session";
import { DisplayProvider } from "./features/display-settings";
import "./styles.css";
const rootRoute = createRootRoute({ component: Shell });
const index = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/overview" });
  },
});
const login = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: () => <Page name="overview" />,
});
const routes = pages.map(([name]) =>
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/" + name,
    component: () => <Page name={name} />,
  }),
);
const router = createRouter({
  routeTree: rootRoute.addChildren([index, login, ...routes]),
  defaultPreload: "intent",
});
const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      refetchOnWindowFocus: false,
      refetchOnReconnect: false,
      retry: false,
    },
  },
});
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={queryClient}>
    <DisplayProvider>
      <SessionProvider>
        <RouterProvider router={router} />
      </SessionProvider>
    </DisplayProvider>
  </QueryClientProvider>,
);
