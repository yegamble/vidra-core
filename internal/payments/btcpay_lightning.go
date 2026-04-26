package payments

// FindLightningMethod searches a slice of InvoicePaymentMethod for the
// Lightning entry. Returns nil if no Lightning method is present or activated.
func FindLightningMethod(methods []InvoicePaymentMethod) *InvoicePaymentMethod {
	for i := range methods {
		if methods[i].PaymentMethod == PaymentMethodLightning && methods[i].Activated {
			return &methods[i]
		}
	}
	return nil
}

// FindOnChainMethod searches a slice of InvoicePaymentMethod for the on-chain
// (BTC) entry. Returns nil if no on-chain method is activated on the invoice.
func FindOnChainMethod(methods []InvoicePaymentMethod) *InvoicePaymentMethod {
	for i := range methods {
		if methods[i].PaymentMethod == PaymentMethodOnChain && methods[i].Activated {
			return &methods[i]
		}
	}
	return nil
}

// PaymentMethodsForRequest converts a payment_method API string
// ("on_chain" | "lightning" | "both") into the BTCPay Greenfield
// checkout.paymentMethods slice. An empty/unknown input maps to on-chain
// only (back-compat with Phase 8A callers that omit the flag).
func PaymentMethodsForRequest(paymentMethod string) []string {
	switch paymentMethod {
	case "lightning":
		return []string{PaymentMethodLightning}
	case "both":
		return []string{PaymentMethodOnChain, PaymentMethodLightning}
	case "on_chain", "":
		return []string{PaymentMethodOnChain}
	default:
		return []string{PaymentMethodOnChain}
	}
}
