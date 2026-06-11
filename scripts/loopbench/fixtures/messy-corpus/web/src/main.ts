// Storefront entrypoint: side-effect imports register all UI actions.
import "./orders/actions";
import { wireCheckout } from "./ui/checkout";

wireCheckout(document.getElementById("checkout-btn"));
