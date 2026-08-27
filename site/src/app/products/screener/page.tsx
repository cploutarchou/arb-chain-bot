import { ProductPage } from "@/components/ProductPage";
import { pageMeta } from "@/lib/meta";

export const metadata = pageMeta(
  "Cross-venue screener",
  "Every pair on every venue, ask against bid, fees subtracted, data age checked.",
  "/products/screener",
);

export default function Page() {
  return <ProductPage name="product-screener" jurisdiction={false} />;
}
