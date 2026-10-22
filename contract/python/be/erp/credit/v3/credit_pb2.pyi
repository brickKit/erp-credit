from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class BranchState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    BRANCH_STATE_UNSPECIFIED: _ClassVar[BranchState]
    BRANCH_STATE_HELD: _ClassVar[BranchState]
    BRANCH_STATE_COMMITTED: _ClassVar[BranchState]
    BRANCH_STATE_CONSUMED: _ClassVar[BranchState]
    BRANCH_STATE_RELEASED: _ClassVar[BranchState]
    BRANCH_STATE_EXPIRED: _ClassVar[BranchState]
    BRANCH_STATE_REJECTED: _ClassVar[BranchState]
    BRANCH_STATE_CANCELLED: _ClassVar[BranchState]
    BRANCH_STATE_ABSENT: _ClassVar[BranchState]
BRANCH_STATE_UNSPECIFIED: BranchState
BRANCH_STATE_HELD: BranchState
BRANCH_STATE_COMMITTED: BranchState
BRANCH_STATE_CONSUMED: BranchState
BRANCH_STATE_RELEASED: BranchState
BRANCH_STATE_EXPIRED: BranchState
BRANCH_STATE_REJECTED: BranchState
BRANCH_STATE_CANCELLED: BranchState
BRANCH_STATE_ABSENT: BranchState

class Reference(_message.Message):
    __slots__ = ("kind", "id", "revision")
    KIND_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    REVISION_FIELD_NUMBER: _ClassVar[int]
    kind: str
    id: str
    revision: int
    def __init__(self, kind: _Optional[str] = ..., id: _Optional[str] = ..., revision: _Optional[int] = ...) -> None: ...

class HoldRequest(_message.Message):
    __slots__ = ("branch_id", "partner_id", "legal_entity_id", "currency", "amount", "reference")
    BRANCH_ID_FIELD_NUMBER: _ClassVar[int]
    PARTNER_ID_FIELD_NUMBER: _ClassVar[int]
    LEGAL_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    CURRENCY_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    REFERENCE_FIELD_NUMBER: _ClassVar[int]
    branch_id: str
    partner_id: str
    legal_entity_id: str
    currency: str
    amount: str
    reference: Reference
    def __init__(self, branch_id: _Optional[str] = ..., partner_id: _Optional[str] = ..., legal_entity_id: _Optional[str] = ..., currency: _Optional[str] = ..., amount: _Optional[str] = ..., reference: _Optional[_Union[Reference, _Mapping]] = ...) -> None: ...

class ConfirmRequest(_message.Message):
    __slots__ = ("branch_id",)
    BRANCH_ID_FIELD_NUMBER: _ClassVar[int]
    branch_id: str
    def __init__(self, branch_id: _Optional[str] = ...) -> None: ...

class CancelRequest(_message.Message):
    __slots__ = ("branch_id",)
    BRANCH_ID_FIELD_NUMBER: _ClassVar[int]
    branch_id: str
    def __init__(self, branch_id: _Optional[str] = ...) -> None: ...

class QueryRequest(_message.Message):
    __slots__ = ("branch_id",)
    BRANCH_ID_FIELD_NUMBER: _ClassVar[int]
    branch_id: str
    def __init__(self, branch_id: _Optional[str] = ...) -> None: ...

class ConsumeRequest(_message.Message):
    __slots__ = ("consumption_id", "branch_id", "amount")
    CONSUMPTION_ID_FIELD_NUMBER: _ClassVar[int]
    BRANCH_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    consumption_id: str
    branch_id: str
    amount: str
    def __init__(self, consumption_id: _Optional[str] = ..., branch_id: _Optional[str] = ..., amount: _Optional[str] = ...) -> None: ...

class AdjustRequest(_message.Message):
    __slots__ = ("branch_id", "revision", "amount")
    BRANCH_ID_FIELD_NUMBER: _ClassVar[int]
    REVISION_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    branch_id: str
    revision: int
    amount: str
    def __init__(self, branch_id: _Optional[str] = ..., revision: _Optional[int] = ..., amount: _Optional[str] = ...) -> None: ...

class Branch(_message.Message):
    __slots__ = ("branch_id", "state", "reason", "partner_id", "legal_entity_id", "currency", "amount", "consumed", "available", "hold_expires_at", "version")
    BRANCH_ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    PARTNER_ID_FIELD_NUMBER: _ClassVar[int]
    LEGAL_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    CURRENCY_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    CONSUMED_FIELD_NUMBER: _ClassVar[int]
    AVAILABLE_FIELD_NUMBER: _ClassVar[int]
    HOLD_EXPIRES_AT_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    branch_id: str
    state: BranchState
    reason: str
    partner_id: str
    legal_entity_id: str
    currency: str
    amount: str
    consumed: str
    available: str
    hold_expires_at: str
    version: int
    def __init__(self, branch_id: _Optional[str] = ..., state: _Optional[_Union[BranchState, str]] = ..., reason: _Optional[str] = ..., partner_id: _Optional[str] = ..., legal_entity_id: _Optional[str] = ..., currency: _Optional[str] = ..., amount: _Optional[str] = ..., consumed: _Optional[str] = ..., available: _Optional[str] = ..., hold_expires_at: _Optional[str] = ..., version: _Optional[int] = ...) -> None: ...

class HoldResponse(_message.Message):
    __slots__ = ("branch",)
    BRANCH_FIELD_NUMBER: _ClassVar[int]
    branch: Branch
    def __init__(self, branch: _Optional[_Union[Branch, _Mapping]] = ...) -> None: ...

class ConfirmResponse(_message.Message):
    __slots__ = ("branch",)
    BRANCH_FIELD_NUMBER: _ClassVar[int]
    branch: Branch
    def __init__(self, branch: _Optional[_Union[Branch, _Mapping]] = ...) -> None: ...

class CancelResponse(_message.Message):
    __slots__ = ("branch",)
    BRANCH_FIELD_NUMBER: _ClassVar[int]
    branch: Branch
    def __init__(self, branch: _Optional[_Union[Branch, _Mapping]] = ...) -> None: ...

class QueryResponse(_message.Message):
    __slots__ = ("branch",)
    BRANCH_FIELD_NUMBER: _ClassVar[int]
    branch: Branch
    def __init__(self, branch: _Optional[_Union[Branch, _Mapping]] = ...) -> None: ...

class ConsumeResponse(_message.Message):
    __slots__ = ("branch",)
    BRANCH_FIELD_NUMBER: _ClassVar[int]
    branch: Branch
    def __init__(self, branch: _Optional[_Union[Branch, _Mapping]] = ...) -> None: ...

class AdjustResponse(_message.Message):
    __slots__ = ("branch",)
    BRANCH_FIELD_NUMBER: _ClassVar[int]
    branch: Branch
    def __init__(self, branch: _Optional[_Union[Branch, _Mapping]] = ...) -> None: ...

class AvailableRequest(_message.Message):
    __slots__ = ("partner_id", "legal_entity_id", "currency")
    PARTNER_ID_FIELD_NUMBER: _ClassVar[int]
    LEGAL_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    CURRENCY_FIELD_NUMBER: _ClassVar[int]
    partner_id: str
    legal_entity_id: str
    currency: str
    def __init__(self, partner_id: _Optional[str] = ..., legal_entity_id: _Optional[str] = ..., currency: _Optional[str] = ...) -> None: ...

class AvailableResponse(_message.Message):
    __slots__ = ("has_limit", "policy", "limit", "order_exposure", "receivable_exposure", "available", "state", "receivables_tracked")
    HAS_LIMIT_FIELD_NUMBER: _ClassVar[int]
    POLICY_FIELD_NUMBER: _ClassVar[int]
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    ORDER_EXPOSURE_FIELD_NUMBER: _ClassVar[int]
    RECEIVABLE_EXPOSURE_FIELD_NUMBER: _ClassVar[int]
    AVAILABLE_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    RECEIVABLES_TRACKED_FIELD_NUMBER: _ClassVar[int]
    has_limit: bool
    policy: str
    limit: str
    order_exposure: str
    receivable_exposure: str
    available: str
    state: str
    receivables_tracked: bool
    def __init__(self, has_limit: _Optional[bool] = ..., policy: _Optional[str] = ..., limit: _Optional[str] = ..., order_exposure: _Optional[str] = ..., receivable_exposure: _Optional[str] = ..., available: _Optional[str] = ..., state: _Optional[str] = ..., receivables_tracked: _Optional[bool] = ...) -> None: ...
