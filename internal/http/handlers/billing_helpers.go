package handlers

// appendPaymentMethod adds newMethod to the payment_methods array stored in metadata,
// deduplicating by last4 (newest replaces oldest for same card).
func appendPaymentMethod(existing any, newMethod map[string]any, last4 string) []any {
	var methods []any
	if arr, ok := existing.([]any); ok {
		for _, m := range arr {
			if mm, ok := m.(map[string]any); ok {
				if l4, _ := mm["last4"].(string); l4 == last4 {
					continue // replace existing entry for this card
				}
			}
			methods = append(methods, m)
		}
	}
	return append(methods, newMethod)
}
