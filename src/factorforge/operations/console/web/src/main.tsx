import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createRootRoute, createRoute, createRouter, RouterProvider, redirect } from "@tanstack/react-router";
import { Shell } from "./components/shell";
import { FlowPage } from "./features/flow-page";
import { MarketPage } from "./features/market-page";
import { SessionProvider } from "./features/session";
import { DisplayProvider } from "./features/display-settings";
import { ThemeProvider } from "./features/theme";
import "./styles.css";
const rootRoute = createRootRoute({ component: Shell });
const home = () => {
  throw redirect({ to: "/flow" });
};
const index = createRoute({ getParentRoute: () => rootRoute, path: "/", beforeLoad: home });
const login = createRoute({ getParentRoute: () => rootRoute, path: "/login", beforeLoad: home });
const flow = createRoute({ getParentRoute: () => rootRoute, path: "/flow", component: FlowPage });
const market = createRoute({ getParentRoute: () => rootRoute, path: "/market", component: MarketPage });
// Old page addresses land on the two pages that replaced them.
const old = ["overview", "dynamic", "events", "sentiment", "decisions", "execution", "cases", "learning", "operations", "audit"].map((path) =>
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/" + path,
    beforeLoad: () => {
      throw redirect({ to: path === "dynamic" || path === "execution" ? "/market" : "/flow" });
    },
  }),
);
const router = createRouter({ routeTree: rootRoute.addChildren([index, login, flow, market, ...old]), defaultPreload: "intent" });
const queryClient = new QueryClient({
  defaultOptions: { queries: { refetchOnWindowFocus: false, refetchOnReconnect: false, retry: false } },
});
createRoot(document.getElementById("root")!).render(
  <QueryClientProvider client={queryClient}>
    <ThemeProvider>
      <DisplayProvider>
        <SessionProvider>
          <RouterProvider router={router} />
        </SessionProvider>
      </DisplayProvider>
    </ThemeProvider>
  </QueryClientProvider>,
);
