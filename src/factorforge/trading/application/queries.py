"""Public projections use explicit business fields rather than internal snapshots."""
def order_view_data(order):
    return {"order_id": order.order_id, "external_order_id": order.external_order_id,
            "client_order_id": order.client_order_id, "owner_id": order.request.owner_id,
            "instrument_key": order.request.instrument_key, "side": order.request.side,
            "requested_quantity": order.request.quantity, "filled_quantity": order.filled_quantity,
            "remaining_quantity": order.remaining, "average_fill_price": order.average_fill_price,
            "state": order.state, "reduce_only": order.request.reduce_only, "spec_version": order.request.spec_version}
