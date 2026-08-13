Fixed five edit controls that accepted a change and then discarded it.

A team's organization, an inventory's organization, and a template's kind, definition
and inventory are each read from storage when the record is updated, which is
deliberate: moving any of them re-scopes every grant or re-tenants every job that
depends on it, and that is a migration rather than an edit. Every one of them was
rendered in the edit form anyway. An operator could pick a different value, submit, be
told it saved, and nothing would have moved.

Form fields can now be declared immutable. An immutable field appears when the record is
created, is absent from the edit form, and is refused rather than silently dropped if a
submission carries it to an update: the form never rendered the control, so a submission
containing it did not come from the form.

Two of the five said "set once" in their own help text from the day they shipped. The
control below the sentence is what a reader believes.

Removing a control also changes what the code reading the submission may demand, and
the first version of this change did not: every inventory, team and template edit was
refused with an error naming a control the page no longer rendered. Fixed, and the
whole class is now covered by a conformance assertion applied to every writable view:
fetch the edit form, resubmit exactly the controls it rendered with the values it
prefilled, and require the write to be accepted.
