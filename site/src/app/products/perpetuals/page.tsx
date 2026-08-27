import { ProductPage } from "@/components/ProductPage";
import { pageMeta } from "@/lib/meta";

export const metadata = pageMeta(
  "Perpetuals basis and funding monitor",
  "Basis and funding across venues, predicted kept apart from settled. Information only.",
  "/products/perpetuals",
);

export default function Page() {
  return <ProductPage name="product-perpetuals" jurisdiction={true} />;
}
